package console

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const consoleSessionTTL = 90 * 24 * time.Hour

type Config struct {
	SecretKey                 string
	AdminEmail                string
	AdminPassword             string
	Artifacts                 artifacts.Config
	ChatAttachments           artifacts.Config
	CodexPublicBaseURL        string
	CodexInstallTokenTTL      time.Duration
	CodexWindowsWrapperURL    string
	CodexWindowsWrapperSHA256 string
}

type consoleChatAttachmentPublisher interface {
	UploadBytes(ctx context.Context, payload []byte, objectKey string, mediaType string) (artifacts.PublishedObject, error)
}

type consoleChatAttachmentObjectReader interface {
	ReadObject(ctx context.Context, objectKey string) ([]byte, string, error)
}

type Handler struct {
	cfg                        Config
	store                      storage.Store
	tmpl                       *template.Template
	cloudAgentRuns             *consoleCloudAgentRunRegistry
	newChatAttachmentPublisher func(cfg artifacts.Config) (consoleChatAttachmentPublisher, error)
	newChatAttachmentReader    func(cfg artifacts.Config) (consoleChatAttachmentObjectReader, error)
	probeWorker                func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile) (workerops.ProbeResult, error)
	buildWorkerBootstrap       func(worker domain.WorkerServer, disks []domain.WorkerDataDisk, proxy domain.ProxyProfile) workerops.BootstrapPlan
	executeWorkerBootstrap     func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, plan workerops.BootstrapPlan) (string, error)
	verifyWorkerBootstrap      func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey) (workerops.BootstrapHealth, error)
	ensureCloudAgent           func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error)
	openCloudAgentShell        func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, cols, rows int) (workerops.InteractiveShell, error)
	runCloudAgentCommand      func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, script string) (string, error)
	runCloudAgentChat          func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error)
}

func NewHandler(cfg Config, store storage.Store) *Handler {
	if cfg.CodexInstallTokenTTL <= 0 {
		cfg.CodexInstallTokenTTL = 15 * time.Minute
	}
	if strings.TrimSpace(cfg.CodexWindowsWrapperURL) == "" {
		cfg.CodexWindowsWrapperURL = "/console/codex/artifacts/aiyolo.exe"
	}
	if strings.TrimSpace(cfg.ChatAttachments.ProxyBasePath) == "" {
		cfg.ChatAttachments.ProxyBasePath = "/console/chat/attachments/files"
	}
	return &Handler{cfg: cfg, store: store, tmpl: template.Must(template.New("console").Funcs(templateFuncs()).ParseFS(consoleAssets, "templates/*.html")), cloudAgentRuns: newConsoleCloudAgentRunRegistry(), newChatAttachmentPublisher: func(cfg artifacts.Config) (consoleChatAttachmentPublisher, error) {
		return artifacts.NewPublisher(cfg)
	}, newChatAttachmentReader: func(cfg artifacts.Config) (consoleChatAttachmentObjectReader, error) {
		return artifacts.NewObjectReader(cfg)
	}, probeWorker: workerops.Probe, buildWorkerBootstrap: workerops.BuildBootstrapPlan, executeWorkerBootstrap: workerops.ExecuteBootstrap, verifyWorkerBootstrap: workerops.VerifyBootstrap, ensureCloudAgent: workerops.EnsureCloudAgent, openCloudAgentShell: workerops.OpenCloudAgentShell, runCloudAgentCommand: workerops.RunCloudAgentCommand, runCloudAgentChat: runConsoleCloudAgentChat}
}

func (handler *Handler) Routes() http.Handler {
	router := chi.NewRouter()
	router.Get("/static/console.css", handler.styles)
	router.Get("/static/chat.js", handler.chatScript)
	router.Get("/static/chat-shell.js", handler.chatShellScript)
	router.Get("/static/chat-workspace.js", handler.chatWorkspaceScript)
	router.Get("/locale", handler.setLocale)
	router.Get("/login", handler.loginPage)
	router.Post("/login", handler.login)
	router.Get("/login/{provider}", handler.oauthLogin)
	router.Get("/oauth/{provider}/callback", handler.oauthCallback)
	router.Group(func(protected chi.Router) {
		protected.Use(handler.requireSession)
		protected.Get("/", handler.dashboard)
		protected.Get("/chat", handler.chat)
		protected.Post("/chat", handler.sendChat)
		protected.Get("/chat/shell/ready", handler.chatShellReady)
		protected.Get("/chat/shell", handler.chatShellPage)
		protected.Handle("/chat/shell/ws", http.HandlerFunc(handler.chatShellSocket))
		protected.Get("/chat/workspace/tree", handler.chatWorkspaceTree)
		protected.Get("/chat/workspace/file", handler.chatWorkspaceFile)
		protected.Post("/chat/workspace/file", handler.saveChatWorkspaceFile)
		protected.Post("/chat/environment/ensure", handler.chatEnvironmentEnsure)
		protected.Post("/chat/session", handler.saveChatSession)
		protected.Delete("/chat/session/{sessionID}", handler.deleteChatSession)
		protected.Post("/chat/stream", handler.streamChat)
		protected.Get("/chat/stream/resume", handler.resumeCloudAgentChatStream)
		protected.Post("/chat/attachments", handler.uploadChatAttachments)
		protected.Get(strings.TrimPrefix(handler.cfg.ChatAttachments.NormalizedProxyBasePath(), "/console")+"/*", handler.chatAttachmentFile)
		protected.Post("/logout", handler.logout)
		protected.Get("/usage", handler.usage)
		protected.Get("/api-keys", handler.apiKeys)
		protected.Post("/api-keys", handler.createAPIKey)
		protected.Post("/api-keys/{keyID}/rotate", handler.rotateAPIKey)
		protected.Post("/api-keys/{keyID}/disable", handler.disableAPIKey)
		protected.Get("/providers", handler.providers)
		protected.Post("/providers", handler.createProvider)
		protected.Post("/providers/openrouter", handler.createOpenRouter)
		protected.Post("/providers/deepseek", handler.createDeepSeek)
		protected.Post("/providers/{providerID}/sync-models", handler.syncProviderModels)
		protected.Get("/models", handler.models)
		protected.Post("/models", handler.createModel)
		protected.Post("/models/test", handler.testModel)
		protected.Get("/proxies", handler.proxies)
		protected.Post("/proxies", handler.createProxy)
		protected.Get("/workers", handler.workers)
		protected.Post("/workers/ssh-keys", handler.createWorkerSSHKey)
		protected.Post("/workers", handler.createWorkerServer)
		protected.Post("/workers/{id}/probe", handler.probeWorkerServer)
		protected.Post("/workers/{id}/initialize", handler.initializeWorkerServer)
		protected.Get("/workers/{id}/jobs/{jobID}/events", handler.workerJobEvents)
		protected.Get("/billing", handler.billing)
		protected.Get("/users", handler.users)
		protected.Get("/settings", handler.settings)
		protected.Post("/settings/auth", handler.saveAuthSettings)
	})
	return router
}

func (handler *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	handler.renderLoginPage(w, r, "")
}

func (handler *Handler) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	settings, err := handler.consoleAuthSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !settings.LocalPasswordEnabled {
		handler.renderLoginPage(w, r, handler.requestText(r, "当前未启用本地密码登录", "Local password sign-in is disabled"))
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if email != handler.cfg.AdminEmail || password != handler.cfg.AdminPassword {
		handler.renderLoginPage(w, r, handler.requestText(r, "邮箱或密码不正确", "Incorrect email or password"))
		return
	}
	setSessionCookie(w, email, handler.cfg.SecretKey)
	http.Redirect(w, r, "/console/", http.StatusSeeOther)
}

func (handler *Handler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: consoleSessionCookieName, Value: "", Path: "/console", MaxAge: -1, Expires: time.Unix(0, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	clearOAuthStateCookie(w)
	http.Redirect(w, r, "/console/login", http.StatusSeeOther)
}

func (handler *Handler) setLocale(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, sanitizeConsoleNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (handler *Handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(consoleSessionCookieName)
		if err != nil || !verifySessionCookie(cookie.Value, handler.cfg.SecretKey) {
			http.Redirect(w, r, "/console/login", http.StatusSeeOther)
			return
		}
		subject := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
		if subject == "" {
			http.Redirect(w, r, "/console/login", http.StatusSeeOther)
			return
		}
		setSessionCookie(w, subject, handler.cfg.SecretKey)
		next.ServeHTTP(w, r)
	})
}

func (handler *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	data, err := handler.store.Dashboard(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	history, err := handler.store.ListUsage(r.Context(), 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, r, "dashboard", map[string]any{"Title": "Dashboard", "Data": data, "ChartUsage": history})
}

func (handler *Handler) usage(w http.ResponseWriter, r *http.Request) {
	history, err := handler.store.ListUsage(r.Context(), 300)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	billing, err := handler.store.BillingOverview(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := history
	if len(items) > 100 {
		items = items[:100]
	}
	handler.render(w, r, "usage", map[string]any{"Title": "Usage", "Usage": items, "Billing": billing, "ChartUsage": history})
}

func (handler *Handler) apiKeys(w http.ResponseWriter, r *http.Request) {
	handler.renderAPIKeysPage(w, r, "", "")
}

func (handler *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	clear, key, err := newConsoleAPIKey(apiKeySpec{
		Name:               formDefault(r, "name", "console key"),
		Kind:               strings.TrimSpace(r.FormValue("kind")),
		AllowedProtocols:   splitCSV(r.FormValue("allowed_protocols")),
		AllowedModels:      splitCSV(r.FormValue("allowed_models")),
		RPMLimit:           formInt(r, "rpm_limit", 0),
		TPMLimit:           formInt(r, "tpm_limit", 0),
		ConcurrentLimit:    formInt(r, "concurrent_limit", 0),
		DailyBudgetCents:   int64(formInt(r, "daily_budget_cents", 0)),
		MonthlyBudgetCents: int64(formInt(r, "monthly_budget_cents", 0)),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := handler.store.CreateAPIKey(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.renderAPIKeysPage(w, r, clear, "")
	return
}

func (handler *Handler) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	key, err := handler.apiKeyByID(r.Context(), chi.URLParam(r, "keyID"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	kind := "live"
	if strings.HasPrefix(strings.TrimSpace(key.Prefix), "aiyolo_test_") {
		kind = "test"
	}
	clear, err := auth.GenerateAPIKey(kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key.KeyHash = auth.HashAPIKey(clear)
	key.Prefix = auth.Prefix(clear)
	if key.Status == "" {
		key.Status = domain.StatusActive
	}
	if err := handler.store.CreateAPIKey(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.renderAPIKeysPage(w, r, clear, handler.requestText(r, "API 密钥已轮换", "API key rotated"))
	return
}

func (handler *Handler) disableAPIKey(w http.ResponseWriter, r *http.Request) {
	key, err := handler.apiKeyByID(r.Context(), chi.URLParam(r, "keyID"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	key.Status = domain.StatusDisabled
	if err := handler.store.CreateAPIKey(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.renderAPIKeysPage(w, r, "", handler.requestText(r, "API 密钥已停用", "API key disabled"))
}

func (handler *Handler) providers(w http.ResponseWriter, r *http.Request) {
	data, err := handler.providersViewData(r.Context(), r, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "providers-content", data)
		return
	}
	handler.render(w, r, "providers", data)
}

func (handler *Handler) createProvider(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	providerID := formDefault(r, "id", newConsoleID("provider"))
	existing := domain.Provider{}
	if current, err := handler.store.GetProvider(r.Context(), providerID); err == nil {
		existing = current
	} else if err != storage.ErrNotFound {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	provider := existing
	provider.ID = providerID
	provider.Name = formDefault(r, "name", firstNonEmpty(existing.Name, "Provider"))
	provider.BaseURL = strings.TrimSpace(r.FormValue("base_url"))
	provider.Protocol = formDefault(r, "protocol", firstNonEmpty(existing.Protocol, domain.ProtocolOpenAI))
	provider.SupportedProtocols = normalizeProviderSupportedProtocols(provider.Protocol, r.Form["supported_protocols"])
	masterKey := strings.TrimSpace(r.FormValue("master_key"))
	if masterKey != "" || strings.TrimSpace(existing.MasterKey) == "" {
		provider.MasterKey = masterKey
	}
	provider.DefaultProxyID = strings.TrimSpace(r.FormValue("default_proxy_id"))
	provider.Status = formDefault(r, "status", firstNonEmpty(existing.Status, domain.StatusEnabled))
	provider.TimeoutSeconds = formInt(r, "timeout_seconds", domain.EffectiveProviderTimeoutSeconds(existing))
	provider.StreamIdleTimeoutSeconds = formInt(r, "stream_idle_timeout_seconds", domain.EffectiveProviderStreamIdleTimeoutSeconds(existing))
	provider.Priority = formInt(r, "priority", defaultProviderInt(existing.Priority, 1))
	provider.Weight = formInt(r, "weight", defaultProviderInt(existing.Weight, 100))
	if err := handler.store.UpsertProvider(r.Context(), provider); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := handler.providersViewData(r.Context(), r, handler.requestText(r, "Provider 已保存", "Provider saved"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "providers-content", data)
		return
	}
	handler.render(w, r, "providers", data)
}

func (handler *Handler) createOpenRouter(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	apiKey := strings.TrimSpace(r.FormValue("master_key"))
	if apiKey == "" {
		http.Error(w, handler.requestText(r, "缺少 master_key", "missing master_key"), http.StatusBadRequest)
		return
	}

	handler.upsertCompatibleProviderAndSync(w, r, domain.Provider{
		ID:                       "openrouter",
		Name:                     "OpenRouter",
		BaseURL:                  "https://openrouter.ai/api/v1",
		Protocol:                 domain.ProtocolOpenAI,
		MasterKey:                apiKey,
		Status:                   domain.StatusEnabled,
		TimeoutSeconds:           180,
		StreamIdleTimeoutSeconds: domain.DefaultProviderStreamIdleTimeoutSeconds,
		Priority:                 1,
		Weight:                   100,
	})
}

func (handler *Handler) createDeepSeek(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	apiKey := strings.TrimSpace(r.FormValue("master_key"))
	if apiKey == "" {
		http.Error(w, handler.requestText(r, "缺少 master_key", "missing master_key"), http.StatusBadRequest)
		return
	}
	handler.upsertCompatibleProviderAndSync(w, r, domain.Provider{
		ID:                       "deepseek",
		Name:                     "DeepSeek",
		BaseURL:                  "https://api.deepseek.com",
		Protocol:                 domain.ProtocolOpenAI,
		SupportedProtocols:       []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic},
		MasterKey:                apiKey,
		Status:                   domain.StatusEnabled,
		TimeoutSeconds:           180,
		StreamIdleTimeoutSeconds: domain.DefaultProviderStreamIdleTimeoutSeconds,
		Priority:                 1,
		Weight:                   100,
	})
}

func (handler *Handler) upsertCompatibleProviderAndSync(w http.ResponseWriter, r *http.Request, provider domain.Provider) {
	if err := handler.store.UpsertProvider(r.Context(), provider); err != nil {
		http.Error(w, fmt.Errorf("upsert provider: %w", err).Error(), http.StatusInternalServerError)
		return
	}
	usdToCNYExchangeRate, err := handler.consoleUSDCNYExchangeRate(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	summary, err := syncCompatibleModelRoutesWithRate(r.Context(), handler.store, provider, usdToCNYExchangeRate)
	if err != nil {
		http.Error(w, fmt.Errorf("sync models: %w", err).Error(), http.StatusInternalServerError)
		return
	}
	data, err := handler.providersViewData(r.Context(), r, handler.providerSyncNotice(r, provider, summary, true))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "providers-content", data)
		return
	}
	handler.render(w, r, "providers", data)
}

func (handler *Handler) syncProviderModels(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(chi.URLParam(r, "providerID"))
	if providerID == "" {
		http.Error(w, handler.requestText(r, "缺少 providerID", "missing providerID"), http.StatusBadRequest)
		return
	}

	provider, err := handler.store.GetProvider(r.Context(), providerID)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, handler.requestText(r, "找不到指定的 Provider。", "The requested provider was not found."), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Errorf("get provider: %w", err).Error(), http.StatusInternalServerError)
		return
	}
	if !supportsCompatibleModelImportProvider(provider) {
		http.Error(w, handler.requestText(r, "当前 Provider 暂不支持自动导入模型。", "The selected provider does not support automatic model import."), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(provider.MasterKey) == "" {
		http.Error(w, handler.requestText(r, "当前 Provider 还没有保存可用的 master key。", "The selected provider does not have a usable master key saved yet."), http.StatusBadRequest)
		return
	}

	usdToCNYExchangeRate, err := handler.consoleUSDCNYExchangeRate(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	summary, err := syncCompatibleModelRoutesWithRate(r.Context(), handler.store, provider, usdToCNYExchangeRate)
	if err != nil {
		http.Error(w, fmt.Errorf("sync models: %w", err).Error(), http.StatusInternalServerError)
		return
	}

	data, err := handler.providersViewData(r.Context(), r, handler.providerSyncNotice(r, provider, summary, false))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "providers-content", data)
		return
	}
	handler.render(w, r, "providers", data)
}

func (handler *Handler) models(w http.ResponseWriter, r *http.Request) {
	data, err := handler.modelsViewData(r.Context(), r, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "models-content", data)
		return
	}
	handler.render(w, r, "models", data)
}

func (handler *Handler) createModel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	route := domain.ModelRoute{PublicName: strings.TrimSpace(r.FormValue("public_name")), ProviderID: strings.TrimSpace(r.FormValue("provider_id")), UpstreamModel: strings.TrimSpace(r.FormValue("upstream_model")), Protocol: formDefault(r, "protocol", domain.ProtocolOpenAI), ProxyProfileID: strings.TrimSpace(r.FormValue("proxy_profile_id")), Enabled: r.FormValue("enabled") != "false", Priority: formInt(r, "priority", 1), Weight: formInt(r, "weight", 100), ContextTokens: formInt(r, "context_tokens", 0)}
	if existing, err := handler.store.LookupModelRoute(r.Context(), route.PublicName); err == nil {
		route.CreatedAt = existing.CreatedAt
		if existing.PriceRuleID != "" && existing.ProviderID == route.ProviderID && existing.UpstreamModel == route.UpstreamModel {
			route.PriceRuleID = existing.PriceRuleID
		}
	} else if err != storage.ErrNotFound {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := handler.store.UpsertModelRoute(r.Context(), route); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := handler.modelsViewData(r.Context(), r, handler.requestText(r, "模型路由已保存", "Model route saved"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "models-content", data)
		return
	}
	handler.render(w, r, "models", data)
}

func (handler *Handler) testModel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	started := time.Now()
	requestID := requestID(r)
	consoleUserID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)

	testForm := modelTestFormView{
		PublicName: strings.TrimSpace(r.FormValue("test_public_name")),
		Prompt:     strings.TrimSpace(r.FormValue("test_prompt")),
	}
	if testForm.Prompt == "" {
		testForm.Prompt = defaultModelTestPrompt(resolveConsoleLocale(r))
	}

	var (
		result       *modelTestResultView
		errorMessage string
	)

	if testForm.PublicName == "" {
		errorMessage = handler.requestText(r, "先选择一个已保存的 public model。", "Select a saved public model first.")
	} else {
		route, err := handler.store.GetModelRoute(r.Context(), testForm.PublicName)
		if err != nil {
			if err == storage.ErrNotFound {
				errorMessage = handler.requestText(r, "找不到这个模型路由，请先保存后再测试。", "That model route was not found. Save it before testing.")
			} else {
				errorMessage = err.Error()
			}
		} else {
			provider, err := handler.store.GetProvider(r.Context(), route.ProviderID)
			if err != nil {
				if err == storage.ErrNotFound {
					errorMessage = handler.requestText(r, "路由引用的 Provider 不存在。", "The provider referenced by this route does not exist.")
				} else {
					errorMessage = err.Error()
				}
			} else if firstNonEmpty(provider.Protocol, route.Protocol, domain.ProtocolOpenAI) != domain.ProtocolOpenAI {
				errorMessage = handler.requestText(r, "当前测试框只支持 OpenAI / OpenRouter 兼容 Provider。", "The test box currently supports only OpenAI/OpenRouter-compatible providers.")
			} else if strings.TrimSpace(provider.MasterKey) == "" {
				errorMessage = handler.requestText(r, "当前 Provider 还没有保存可用的 master key。", "The selected provider does not have a usable master key saved yet.")
			} else {
				protocol := firstNonEmpty(provider.Protocol, route.Protocol, domain.ProtocolOpenAI)
				pricingRule, err := resolveModelTestPricingRule(r.Context(), handler.store, route)
				if err != nil {
					errorMessage = fmt.Sprintf(handler.requestText(r, "测试失败：%s", "Test failed: %s"), err.Error())
				} else {
					profile, err := resolveModelTestProxyProfile(r.Context(), handler.store, provider, route)
					if err != nil {
						errorMessage = fmt.Sprintf(handler.requestText(r, "测试失败：%s", "Test failed: %s"), err.Error())
					} else {
						view, err := runModelRouteTest(r.Context(), provider, route, profile, testForm.Prompt)
						persistModelTestOutcome(context.WithoutCancel(r.Context()), handler.store, requestID, consoleUserID, protocol, route, provider, pricingRule, started, view)
						if err != nil {
							errorMessage = fmt.Sprintf(handler.requestText(r, "测试失败：%s", "Test failed: %s"), err.Error())
						} else {
							result = &view.Result
						}
					}
				}
			}
		}
	}

	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "model-test-result", map[string]any{"TestForm": testForm, "TestResult": result, "TestError": errorMessage})
		return
	}

	data, err := handler.modelsViewData(r.Context(), r, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["TestForm"] = testForm
	data["TestResult"] = result
	data["TestError"] = errorMessage
	if errorMessage != "" {
		data["Error"] = errorMessage
	}
	handler.render(w, r, "models", data)
}

func (handler *Handler) proxies(w http.ResponseWriter, r *http.Request) {
	data, err := handler.proxiesViewData(r.Context(), r, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isLockedProxyProfileID(r.URL.Query().Get("edit_proxy_id")) {
		data["Error"] = handler.requestText(r, "内置 direct Profile 不可编辑", "The built-in direct profile cannot be edited")
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "proxies-content", data)
		return
	}
	handler.render(w, r, "proxies", data)
}

func (handler *Handler) createProxy(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if isLockedProxyProfileID(r.FormValue("id")) {
		http.Error(w, handler.requestText(r, "内置 direct Profile 不可编辑", "The built-in direct profile cannot be edited"), http.StatusBadRequest)
		return
	}
	profile, err := domain.NormalizeProxyProfile(domain.ProxyProfile{ID: formDefault(r, "id", newConsoleID("proxy")), Name: formDefault(r, "name", "Proxy"), Type: formDefault(r, "type", domain.ProxyTypeDirect), Endpoint: strings.TrimSpace(r.FormValue("endpoint")), Auth: strings.TrimSpace(r.FormValue("auth")), Region: strings.TrimSpace(r.FormValue("region")), TimeoutSeconds: formInt(r, "timeout_seconds", domain.DefaultProxyTimeoutSeconds), StreamIdleTimeoutSeconds: formInt(r, "stream_idle_timeout_seconds", domain.DefaultProxyStreamIdleTimeoutSeconds), HealthCheckURL: strings.TrimSpace(r.FormValue("health_check_url")), Status: formDefault(r, "status", domain.StatusEnabled)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := handler.store.UpsertProxyProfile(r.Context(), profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := handler.proxiesViewData(r.Context(), r, handler.requestText(r, "代理 Profile 已保存", "Proxy profile saved"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "proxies-content", data)
		return
	}
	handler.render(w, r, "proxies", data)
}

func (handler *Handler) billing(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/console/usage#spend-overview", http.StatusSeeOther)
}

func (handler *Handler) users(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/console/settings#identity-center", http.StatusSeeOther)
}

func (handler *Handler) settings(w http.ResponseWriter, r *http.Request) {
	handler.renderSettingsPage(w, r, "", "")
}

func (handler *Handler) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := handler.tmpl.ExecuteTemplate(w, name, handler.decoratePageData(r, data)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (handler *Handler) renderFragment(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := handler.tmpl.ExecuteTemplate(w, name, handler.decoratePageData(r, data)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (handler *Handler) styles(w http.ResponseWriter, r *http.Request) {
	css, err := consoleAssets.ReadFile("static/console.css")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(css)
}

func (handler *Handler) chatScript(w http.ResponseWriter, r *http.Request) {
	script, err := consoleAssets.ReadFile("static/chat.js")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(script)
}

func (handler *Handler) chatShellScript(w http.ResponseWriter, r *http.Request) {
	script, err := consoleAssets.ReadFile("static/chat-shell.js")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(script)
}

func (handler *Handler) chatWorkspaceScript(w http.ResponseWriter, r *http.Request) {
	script, err := consoleAssets.ReadFile("static/chat-workspace.js")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(script)
}

func (handler *Handler) apiKeysPageData(ctx context.Context, createdKey string, notice string) (map[string]any, error) {
	keys, err := handler.store.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := handler.store.ListModelRoutes(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := handler.store.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Title": "API Keys", "Keys": keys, "CreatedKey": createdKey, "ConfiguredProtocols": configuredProtocols(providers), "RouteAliases": routeAliases(routes), "Notice": notice}, nil
}

func (handler *Handler) renderAPIKeysPage(w http.ResponseWriter, r *http.Request, createdKey string, notice string) {
	data, err := handler.apiKeysPageData(r.Context(), createdKey, notice)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "apiKeys-content", data)
		return
	}
	handler.render(w, r, "apiKeys", data)
}

func (handler *Handler) apiKeyByID(ctx context.Context, keyID string) (domain.APIKey, error) {
	keys, err := handler.store.ListAPIKeys(ctx)
	if err != nil {
		return domain.APIKey{}, err
	}
	for _, key := range keys {
		if key.ID == keyID {
			return key, nil
		}
	}
	return domain.APIKey{}, storage.ErrNotFound
}

type apiKeySpec struct {
	ID                 string
	Name               string
	Kind               string
	UserID             string
	OrganizationID     string
	ProjectID          string
	Status             string
	AllowedProtocols   []string
	AllowedModels      []string
	RPMLimit           int
	TPMLimit           int
	ConcurrentLimit    int
	DailyBudgetCents   int64
	MonthlyBudgetCents int64
	ExpiresAt          *time.Time
	CreatedAt          time.Time
}

func newConsoleAPIKey(spec apiKeySpec) (string, domain.APIKey, error) {
	clear, err := auth.GenerateAPIKey(strings.TrimSpace(spec.Kind))
	if err != nil {
		return "", domain.APIKey{}, err
	}
	createdAt := spec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	id := strings.TrimSpace(spec.ID)
	if id == "" {
		id = newConsoleID("key")
	}
	key := domain.APIKey{
		ID:                 id,
		Name:               firstNonEmpty(spec.Name, "console key"),
		KeyHash:            auth.HashAPIKey(clear),
		Prefix:             auth.Prefix(clear),
		UserID:             firstNonEmpty(spec.UserID, "local"),
		OrganizationID:     firstNonEmpty(spec.OrganizationID, "default"),
		ProjectID:          firstNonEmpty(spec.ProjectID, "default"),
		Status:             firstNonEmpty(spec.Status, domain.StatusActive),
		AllowedProtocols:   spec.AllowedProtocols,
		AllowedModels:      spec.AllowedModels,
		RPMLimit:           spec.RPMLimit,
		TPMLimit:           spec.TPMLimit,
		ConcurrentLimit:    spec.ConcurrentLimit,
		DailyBudgetCents:   spec.DailyBudgetCents,
		MonthlyBudgetCents: spec.MonthlyBudgetCents,
		ExpiresAt:          spec.ExpiresAt,
		CreatedAt:          createdAt,
	}
	return clear, key, nil
}

func (handler *Handler) providersPageData(ctx context.Context, notice string) (map[string]any, error) {
	providers, err := handler.store.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	proxies, err := handler.store.ListProxyProfiles(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Title": "Providers", "Providers": providers, "Proxies": proxies, "Notice": notice}, nil
}

func (handler *Handler) providersViewData(ctx context.Context, r *http.Request, notice string) (map[string]any, error) {
	data, err := handler.providersPageData(ctx, notice)
	if err != nil {
		return nil, err
	}
	if err := buildProvidersViewData(ctx, handler.store, data, r); err != nil {
		return nil, err
	}
	return data, nil
}

func (handler *Handler) modelsPageData(ctx context.Context, notice string) (map[string]any, error) {
	routes, err := handler.store.ListModelRoutes(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := handler.store.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	proxies, err := handler.store.ListProxyProfiles(ctx)
	if err != nil {
		return nil, err
	}
	pricingRules, err := handler.store.ListPricingRules(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Title": "Models", "Routes": routes, "Providers": providers, "Proxies": proxies, "PricingRules": pricingRulesByID(pricingRules), "KnownUpstreamModels": upstreamModels(routes), "Notice": notice}, nil
}

func (handler *Handler) modelsViewData(ctx context.Context, r *http.Request, notice string) (map[string]any, error) {
	data, err := handler.modelsPageData(ctx, notice)
	if err != nil {
		return nil, err
	}
	buildModelsViewData(data, r)
	return data, nil
}

func (handler *Handler) proxiesPageData(ctx context.Context, notice string) (map[string]any, error) {
	profiles, err := handler.store.ListProxyProfiles(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Title": "Proxies", "Profiles": profiles, "Regions": proxyRegions(profiles), "Notice": notice}, nil
}

func (handler *Handler) proxiesViewData(ctx context.Context, r *http.Request, notice string) (map[string]any, error) {
	data, err := handler.proxiesPageData(ctx, notice)
	if err != nil {
		return nil, err
	}
	if err := buildProxiesViewData(ctx, handler.store, data, r); err != nil {
		return nil, err
	}
	return data, nil
}

func configuredProtocols(providers []domain.Provider) []string {
	values := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, protocol := range []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic} {
		for _, provider := range providers {
			if provider.Protocol == protocol {
				values = appendUniqueString(values, seen, provider.Protocol)
				break
			}
		}
	}
	for _, provider := range providers {
		values = appendUniqueString(values, seen, provider.Protocol)
	}
	return values
}

func routeAliases(routes []domain.ModelRoute) []string {
	values := make([]string, 0, len(routes))
	seen := make(map[string]struct{})
	for _, route := range routes {
		values = appendUniqueString(values, seen, route.PublicName)
	}
	return values
}

func upstreamModels(routes []domain.ModelRoute) []string {
	values := make([]string, 0, len(routes))
	seen := make(map[string]struct{})
	for _, route := range routes {
		values = appendUniqueString(values, seen, route.UpstreamModel)
	}
	return values
}

func pricingRulesByID(rules []domain.PricingRule) map[string]domain.PricingRule {
	values := make(map[string]domain.PricingRule, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.ID) == "" {
			continue
		}
		values[rule.ID] = rule
	}
	return values
}

func proxyRegions(profiles []domain.ProxyProfile) []string {
	values := make([]string, 0, len(profiles))
	seen := make(map[string]struct{})
	for _, profile := range profiles {
		values = appendUniqueString(values, seen, profile.Region)
	}
	return values
}

func appendUniqueString(values []string, seen map[string]struct{}, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	if _, ok := seen[value]; ok {
		return values
	}
	seen[value] = struct{}{}
	return append(values, value)
}

func (handler *Handler) billingPageData(ctx context.Context) (map[string]any, error) {
	data, err := handler.store.BillingOverview(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Title": "Billing", "Data": data}, nil
}

func (handler *Handler) usersPageData(ctx context.Context) (map[string]any, error) {
	data, err := handler.store.UserDirectory(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Title": "Users", "AdminEmail": handler.cfg.AdminEmail, "Data": data}, nil
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true")
}

func requestID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("x-request-id")); value != "" {
		return value
	}
	return newID("req")
}

func newID(prefix string) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(raw)
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("x-forwarded-for")); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func setSessionCookie(w http.ResponseWriter, email, secret string) {
	expiresAt := time.Now().Add(consoleSessionTTL).UTC()
	expires := expiresAt.Unix()
	value := email + ":" + strconv.FormatInt(expires, 10)
	signature := auth.Sign(value, secret)
	http.SetCookie(w, &http.Cookie{Name: consoleSessionCookieName, Value: value + ":" + signature, Path: "/console", Expires: expiresAt, MaxAge: int(consoleSessionTTL / time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func verifySessionCookie(value, secret string) bool {
	parts := strings.Split(value, ":")
	if len(parts) < 3 {
		return false
	}
	payload := strings.Join(parts[:len(parts)-1], ":")
	signature := parts[len(parts)-1]
	if !auth.Verify(payload, signature, secret) {
		return false
	}
	expires, err := strconv.ParseInt(parts[len(parts)-2], 10, 64)
	return err == nil && time.Now().Unix() < expires
}

func formDefault(r *http.Request, key, fallback string) string {
	if value := strings.TrimSpace(r.FormValue(key)); value != "" {
		return value
	}
	return fallback
}

func formInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.FormValue(key)))
	if err != nil {
		return fallback
	}
	return value
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func newConsoleID(prefix string) string {
	return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func money(microCents int64, currency ...string) string {
	amount := strconv.FormatFloat(float64(microCents)/100000000, 'f', 4, 64)
	code := domain.DefaultBillingCurrency
	if len(currency) > 0 {
		if trimmed := strings.ToUpper(strings.TrimSpace(currency[0])); trimmed != "" {
			code = trimmed
		}
	}
	switch code {
	case domain.CurrencyCNY:
		return "¥" + amount
	case domain.CurrencyUSD:
		return "$" + amount
	default:
		return code + " " + amount
	}
}

func centsMoney(cents int64, currency ...string) string {
	return money(cents*1000000, currency...)
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"displayTitle":                          pageTitleLocalized,
		"centsMoney":                            centsMoney,
		"effectiveModelProxy":                   effectiveModelProxy,
		"modelProxySelectLabel":                 modelProxySelectLabel,
		"msg":                                   consoleText,
		"money":                                 money,
		"countShare":                            countShare,
		"chatEnvironmentLabel":                  consoleChatEnvironmentLabel,
		"join":                                  strings.Join,
		"navClass":                              navClass,
		"orDash":                                orDash,
		"pairShare":                             pairShare,
		"pageEyebrow":                           pageEyebrow,
		"reasoningEffortLabel":                  consoleChatReasoningEffortLabel,
		"isOpenRouterProvider":                  isOpenRouterProvider,
		"providerProtocolSummary":               providerProtocolSummary,
		"protocolLabel":                         protocolLabel,
		"ratio":                                 ratio,
		"supportsProtocol":                      domain.SupportsProtocol,
		"supportsCompatibleModelImportProvider": supportsCompatibleModelImportProvider,
		"statusClass":                           statusClass,
		"statusText":                            statusText,
		"timefmt":                               timefmt,
		"usageArea":                             usageArea,
		"usageSparkline":                        usageSparkline,
	}
}

func navClass(currentTitle, itemTitle string) string {
	if strings.HasPrefix(itemTitle, "/console") {
		if itemTitle == "/console/" && (currentTitle == "/console" || currentTitle == "/console/") {
			return "nav-link is-active"
		}
		if itemTitle != "/console/" && strings.HasPrefix(currentTitle, itemTitle) {
			return "nav-link is-active"
		}
		return "nav-link"
	}
	if currentTitle == itemTitle {
		return "nav-link is-active"
	}
	return "nav-link"
}

func pageEyebrow(title string) string {
	switch title {
	case "Dashboard":
		return "Control Center"
	case "Usage":
		return "Usage Ledger"
	case "API Keys":
		return "Credential Surface"
	case "Providers":
		return "Upstream Channels"
	case "Models":
		return "Route Map"
	case "Proxies":
		return "Network Paths"
	case "Billing":
		return "Spend View"
	case "Users":
		return "Identity Surface"
	case "Settings":
		return "Access Policy"
	case "Login":
		return "Sign In"
	default:
		return "AIYolo Console"
	}
}

func (handler *Handler) providerSyncNotice(r *http.Request, provider domain.Provider, summary openRouterSyncSummary, created bool) string {
	providerName := firstNonEmpty(strings.TrimSpace(provider.Name), strings.TrimSpace(provider.ID), "Provider")
	if summary.SkippedConflicts > 0 {
		if created {
			return fmt.Sprintf(
				handler.requestText(r, "成功接入 %[1]s，导入了 %[2]d 个模型，并保留了 %[3]d 条同名路由。", "%[1]s connected, %[2]d models were imported, and %[3]d conflicting routes were kept."),
				providerName,
				summary.Synced,
				summary.SkippedConflicts,
			)
		}
		return fmt.Sprintf(
			handler.requestText(r, "已从 %[1]s 重新导入 %[2]d 个模型，并保留了 %[3]d 条同名路由。", "Re-imported %[2]d models from %[1]s and kept %[3]d conflicting routes."),
			providerName,
			summary.Synced,
			summary.SkippedConflicts,
		)
	}
	if created {
		return fmt.Sprintf(
			handler.requestText(r, "成功接入 %[1]s，并导入了 %[2]d 个模型", "%[1]s connected and %[2]d models were imported"),
			providerName,
			summary.Synced,
		)
	}
	return fmt.Sprintf(
		handler.requestText(r, "已从 %[1]s 重新导入 %[2]d 个模型", "Re-imported %[2]d models from %[1]s"),
		providerName,
		summary.Synced,
	)
}

func protocolLabel(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case domain.ProtocolOpenAI:
		return "OpenAI"
	case domain.ProtocolAnthropic:
		return "Anthropic"
	case "":
		return "-"
	default:
		return strings.ToUpper(value)
	}
}

func statusClass(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "enabled", "active", "ready", "running", "succeeded":
		return "badge tone-good"
	case "disabled", "failed", "error":
		return "badge tone-danger"
	case "pending", "initializing", "queued", "starting":
		return "badge tone-warn"
	case "degraded":
		return "badge tone-warn"
	default:
		return "badge tone-neutral"
	}
}

func statusText(locale, value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "enabled":
		return consoleText(locale, "已启用", "Enabled")
	case "active":
		return consoleText(locale, "生效中", "Active")
	case "disabled":
		return consoleText(locale, "已停用", "Disabled")
	case "degraded":
		return consoleText(locale, "降级", "Degraded")
	case "ready":
		return consoleText(locale, "就绪", "Ready")
	case "pending":
		return consoleText(locale, "待处理", "Pending")
	case "initializing":
		return consoleText(locale, "初始化中", "Initializing")
	case "failed", "error":
		return consoleText(locale, "失败", "Failed")
	case "queued":
		return consoleText(locale, "排队中", "Queued")
	case "running":
		return consoleText(locale, "运行中", "Running")
	case "succeeded":
		return consoleText(locale, "完成", "Succeeded")
	case "stopped":
		return consoleText(locale, "已停止", "Stopped")
	case "starting":
		return consoleText(locale, "启动中", "Starting")
	case "closed":
		return consoleText(locale, "已关闭", "Closed")
	default:
		return value
	}
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func ratio(part, total int64) string {
	if total <= 0 {
		return "0%"
	}
	return strconv.FormatInt(part*100/total, 10) + "%"
}

func pairShare(left, right int64) string {
	return ratio(left, left+right)
}

func timefmt(value any) string {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return "-"
		}
		return typed.Local().Format("2006-01-02 15:04")
	case *time.Time:
		if typed == nil || typed.IsZero() {
			return "-"
		}
		return typed.Local().Format("2006-01-02 15:04")
	default:
		return "-"
	}
}

func EnsureSeedKey(ctx context.Context, store storage.Store, clearKey string) error {
	if clearKey == "" {
		return nil
	}
	return store.CreateAPIKey(ctx, domain.APIKey{ID: "seed", Name: "Seed API Key", KeyHash: auth.HashAPIKey(clearKey), Prefix: auth.Prefix(clearKey), UserID: "local", OrganizationID: "default", ProjectID: "default", Status: domain.StatusActive, CreatedAt: time.Now().UTC()})
}
