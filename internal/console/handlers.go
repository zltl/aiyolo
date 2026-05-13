package console

import (
	"context"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

type Config struct {
	SecretKey     string
	AdminEmail    string
	AdminPassword string
}

type Handler struct {
	cfg   Config
	store storage.Store
	tmpl  *template.Template
}

func NewHandler(cfg Config, store storage.Store) *Handler {
	return &Handler{cfg: cfg, store: store, tmpl: template.Must(template.New("console").Funcs(template.FuncMap{"money": money, "join": strings.Join}).Parse(templates))}
}

func (handler *Handler) Routes() http.Handler {
	router := chi.NewRouter()
	router.Get("/login", handler.loginPage)
	router.Post("/login", handler.login)
	router.Group(func(protected chi.Router) {
		protected.Use(handler.requireSession)
		protected.Get("/", handler.dashboard)
		protected.Post("/logout", handler.logout)
		protected.Get("/usage", handler.usage)
		protected.Get("/audit", handler.audit)
		protected.Get("/api-keys", handler.apiKeys)
		protected.Post("/api-keys", handler.createAPIKey)
		protected.Get("/providers", handler.providers)
		protected.Post("/providers", handler.createProvider)
		protected.Get("/models", handler.models)
		protected.Post("/models", handler.createModel)
		protected.Get("/proxies", handler.proxies)
		protected.Post("/proxies", handler.createProxy)
		protected.Get("/billing", handler.billing)
		protected.Get("/users", handler.users)
		protected.Get("/settings", handler.settings)
	})
	return router
}

func (handler *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	handler.render(w, "login", map[string]any{"Title": "Login"})
}

func (handler *Handler) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if email != handler.cfg.AdminEmail || password != handler.cfg.AdminPassword {
		handler.render(w, "login", map[string]any{"Title": "Login", "Error": "邮箱或密码不正确"})
		return
	}
	setSessionCookie(w, email, handler.cfg.SecretKey)
	http.Redirect(w, r, "/console/", http.StatusSeeOther)
}

func (handler *Handler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "aiyolo_console", Value: "", Path: "/console", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/console/login", http.StatusSeeOther)
}

func (handler *Handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("aiyolo_console")
		if err != nil || !verifySessionCookie(cookie.Value, handler.cfg.SecretKey) {
			http.Redirect(w, r, "/console/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (handler *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	data, err := handler.store.Dashboard(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, "dashboard", map[string]any{"Title": "Dashboard", "Data": data})
}

func (handler *Handler) usage(w http.ResponseWriter, r *http.Request) {
	items, err := handler.store.ListUsage(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, "usage", map[string]any{"Title": "Usage", "Usage": items})
}

func (handler *Handler) audit(w http.ResponseWriter, r *http.Request) {
	items, err := handler.store.ListAudit(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, "audit", map[string]any{"Title": "Audit", "Audits": items})
}

func (handler *Handler) apiKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := handler.store.ListAPIKeys(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, "apiKeys", map[string]any{"Title": "API Keys", "Keys": keys})
}

func (handler *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	clear, err := auth.GenerateAPIKey(strings.TrimSpace(r.FormValue("kind")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key := domain.APIKey{ID: newConsoleID("key"), Name: formDefault(r, "name", "console key"), KeyHash: auth.HashAPIKey(clear), Prefix: auth.Prefix(clear), UserID: "local", OrganizationID: "default", ProjectID: "default", Status: domain.StatusActive, AllowedProtocols: splitCSV(r.FormValue("allowed_protocols")), AllowedModels: splitCSV(r.FormValue("allowed_models")), RPMLimit: formInt(r, "rpm_limit", 0), TPMLimit: formInt(r, "tpm_limit", 0), ConcurrentLimit: formInt(r, "concurrent_limit", 0), DailyBudgetCents: int64(formInt(r, "daily_budget_cents", 0)), MonthlyBudgetCents: int64(formInt(r, "monthly_budget_cents", 0)), CreatedAt: time.Now().UTC()}
	if err := handler.store.CreateAPIKey(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	keys, _ := handler.store.ListAPIKeys(r.Context())
	handler.render(w, "apiKeys", map[string]any{"Title": "API Keys", "Keys": keys, "CreatedKey": clear})
}

func (handler *Handler) providers(w http.ResponseWriter, r *http.Request) {
	providers, err := handler.store.ListProviders(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, "providers", map[string]any{"Title": "Providers", "Providers": providers})
}

func (handler *Handler) createProvider(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	provider := domain.Provider{ID: formDefault(r, "id", newConsoleID("provider")), Name: formDefault(r, "name", "Provider"), BaseURL: strings.TrimSpace(r.FormValue("base_url")), Protocol: formDefault(r, "protocol", domain.ProtocolOpenAI), MasterKey: strings.TrimSpace(r.FormValue("master_key")), DefaultProxyID: strings.TrimSpace(r.FormValue("default_proxy_id")), Status: formDefault(r, "status", domain.StatusEnabled), TimeoutSeconds: formInt(r, "timeout_seconds", 90), Priority: formInt(r, "priority", 1), Weight: formInt(r, "weight", 100)}
	if err := handler.store.UpsertProvider(r.Context(), provider); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/console/providers", http.StatusSeeOther)
}

func (handler *Handler) models(w http.ResponseWriter, r *http.Request) {
	routes, err := handler.store.ListModelRoutes(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	providers, _ := handler.store.ListProviders(r.Context())
	proxies, _ := handler.store.ListProxyProfiles(r.Context())
	handler.render(w, "models", map[string]any{"Title": "Models", "Routes": routes, "Providers": providers, "Proxies": proxies})
}

func (handler *Handler) createModel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	route := domain.ModelRoute{PublicName: strings.TrimSpace(r.FormValue("public_name")), ProviderID: strings.TrimSpace(r.FormValue("provider_id")), UpstreamModel: strings.TrimSpace(r.FormValue("upstream_model")), Protocol: formDefault(r, "protocol", domain.ProtocolOpenAI), ProxyProfileID: strings.TrimSpace(r.FormValue("proxy_profile_id")), Enabled: r.FormValue("enabled") != "false", Priority: formInt(r, "priority", 1), Weight: formInt(r, "weight", 100), ContextTokens: formInt(r, "context_tokens", 0)}
	if err := handler.store.UpsertModelRoute(r.Context(), route); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/console/models", http.StatusSeeOther)
}

func (handler *Handler) proxies(w http.ResponseWriter, r *http.Request) {
	profiles, err := handler.store.ListProxyProfiles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.render(w, "proxies", map[string]any{"Title": "Proxies", "Profiles": profiles})
}

func (handler *Handler) createProxy(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profile := domain.ProxyProfile{ID: formDefault(r, "id", newConsoleID("proxy")), Name: formDefault(r, "name", "Proxy"), Type: formDefault(r, "type", "direct"), Endpoint: strings.TrimSpace(r.FormValue("endpoint")), Auth: strings.TrimSpace(r.FormValue("auth")), Region: strings.TrimSpace(r.FormValue("region")), TimeoutSeconds: formInt(r, "timeout_seconds", 60), HealthCheckURL: strings.TrimSpace(r.FormValue("health_check_url")), Status: formDefault(r, "status", domain.StatusEnabled)}
	if err := handler.store.UpsertProxyProfile(r.Context(), profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/console/proxies", http.StatusSeeOther)
}

func (handler *Handler) billing(w http.ResponseWriter, r *http.Request) {
	handler.render(w, "placeholder", map[string]any{"Title": "Billing"})
}
func (handler *Handler) users(w http.ResponseWriter, r *http.Request) {
	handler.render(w, "placeholder", map[string]any{"Title": "Users"})
}
func (handler *Handler) settings(w http.ResponseWriter, r *http.Request) {
	handler.render(w, "placeholder", map[string]any{"Title": "Settings"})
}

func (handler *Handler) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := handler.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func setSessionCookie(w http.ResponseWriter, email, secret string) {
	expires := time.Now().Add(12 * time.Hour).Unix()
	value := email + ":" + strconv.FormatInt(expires, 10)
	signature := auth.Sign(value, secret)
	http.SetCookie(w, &http.Cookie{Name: "aiyolo_console", Value: value + ":" + signature, Path: "/console", Expires: time.Unix(expires, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode})
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

func money(microCents int64) string {
	return "$" + strconv.FormatFloat(float64(microCents)/100000000, 'f', 4, 64)
}

func EnsureSeedKey(ctx context.Context, store storage.Store, clearKey string) error {
	if clearKey == "" {
		return nil
	}
	return store.CreateAPIKey(ctx, domain.APIKey{ID: "seed", Name: "Seed API Key", KeyHash: auth.HashAPIKey(clearKey), Prefix: auth.Prefix(clearKey), UserID: "local", OrganizationID: "default", ProjectID: "default", Status: domain.StatusActive, CreatedAt: time.Now().UTC()})
}

const templates = `
{{define "layout-start"}}<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}} - AIYolo</title><script src="https://unpkg.com/htmx.org@1.9.12"></script><style>body{font-family:Inter,system-ui,sans-serif;margin:0;background:#f7f7f8;color:#171717}.shell{display:grid;grid-template-columns:220px 1fr;min-height:100vh}.side{background:#111827;color:#f9fafb;padding:20px}.side a{display:block;color:#d1d5db;text-decoration:none;padding:8px 0}.side a:hover{color:white}.main{padding:24px}.card{background:white;border:1px solid #e5e7eb;border-radius:8px;padding:16px;margin-bottom:16px}.grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.metric{font-size:26px;font-weight:700}table{width:100%;border-collapse:collapse;background:white}th,td{border-bottom:1px solid #e5e7eb;text-align:left;padding:10px;font-size:14px}input,select{box-sizing:border-box;width:100%;padding:8px;border:1px solid #d1d5db;border-radius:6px}button{padding:8px 12px;border:0;border-radius:6px;background:#111827;color:white;cursor:pointer}.formgrid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}.notice{background:#ecfdf5;border:1px solid #a7f3d0;color:#065f46;padding:10px;border-radius:6px;margin-bottom:12px}.error{background:#fef2f2;border:1px solid #fecaca;color:#991b1b;padding:10px;border-radius:6px}</style></head><body><div class="shell"><nav class="side"><h2>AIYolo</h2><a href="/console/">Dashboard</a><a href="/console/usage">Usage</a><a href="/console/audit">Audit</a><a href="/console/api-keys">API Keys</a><a href="/console/providers">Providers</a><a href="/console/models">Models</a><a href="/console/proxies">Proxies</a><a href="/console/billing">Billing</a><a href="/console/users">Users</a><a href="/console/settings">Settings</a><form method="post" action="/console/logout"><button>退出</button></form></nav><main class="main"><h1>{{.Title}}</h1>{{end}}
{{define "layout-end"}}</main></div></body></html>{{end}}
{{define "login"}}<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Login - AIYolo</title><style>body{font-family:Inter,system-ui,sans-serif;background:#f7f7f8;display:grid;place-items:center;min-height:100vh}.box{background:white;border:1px solid #e5e7eb;border-radius:8px;padding:24px;width:360px}input{box-sizing:border-box;width:100%;padding:10px;margin:8px 0;border:1px solid #d1d5db;border-radius:6px}button{width:100%;padding:10px;border:0;border-radius:6px;background:#111827;color:white}.error{background:#fef2f2;color:#991b1b;padding:8px;border-radius:6px}</style></head><body><form class="box" method="post"><h1>AIYolo Console</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<input name="email" placeholder="email" autocomplete="username"><input name="password" placeholder="password" type="password" autocomplete="current-password"><button>登录</button></form></body></html>{{end}}
{{define "dashboard"}}{{template "layout-start" .}}<div class="grid"><div class="card"><div>24h 请求</div><div class="metric">{{.Data.RequestCount}}</div></div><div class="card"><div>错误</div><div class="metric">{{.Data.ErrorCount}}</div></div><div class="card"><div>输入 tokens</div><div class="metric">{{.Data.InputTokens}}</div></div><div class="card"><div>输出 tokens</div><div class="metric">{{.Data.OutputTokens}}</div></div></div><div class="card"><h2>模型费用</h2><table><tr><th>模型</th><th>请求</th><th>输入</th><th>输出</th><th>费用</th></tr>{{range .Data.ModelCosts}}<tr><td>{{.ModelAlias}}</td><td>{{.RequestCount}}</td><td>{{.InputTokens}}</td><td>{{.OutputTokens}}</td><td>{{money .CostMicroCents}}</td></tr>{{end}}</table></div>{{template "layout-end" .}}{{end}}
{{define "usage"}}{{template "layout-start" .}}<table><tr><th>时间</th><th>请求</th><th>模型</th><th>Provider</th><th>状态</th><th>输入</th><th>输出</th><th>耗时</th></tr>{{range .Usage}}<tr><td>{{.CreatedAt}}</td><td>{{.RequestID}}</td><td>{{.ModelAlias}}</td><td>{{.ProviderID}}</td><td>{{.StatusCode}}</td><td>{{.InputTokens}}</td><td>{{.OutputTokens}}</td><td>{{.LatencyMS}}ms</td></tr>{{end}}</table>{{template "layout-end" .}}{{end}}
{{define "audit"}}{{template "layout-start" .}}<table><tr><th>时间</th><th>事件</th><th>请求</th><th>模型</th><th>状态</th><th>错误</th><th>代理</th></tr>{{range .Audits}}<tr><td>{{.CreatedAt}}</td><td>{{.EventType}}</td><td>{{.RequestID}}</td><td>{{.ModelAlias}}</td><td>{{.StatusCode}}</td><td>{{.ErrorCode}}</td><td>{{.ProxyProfileID}}</td></tr>{{end}}</table>{{template "layout-end" .}}{{end}}
{{define "apiKeys"}}{{template "layout-start" .}}{{if .CreatedKey}}<div class="notice">新 API Key 只显示一次：<strong>{{.CreatedKey}}</strong></div>{{end}}<div class="card"><form method="post" class="formgrid"><input name="name" placeholder="名称"><input name="kind" placeholder="live 或 test" value="live"><input name="allowed_protocols" placeholder="允许协议，空为全部"><input name="allowed_models" placeholder="允许模型，空为全部"><input name="rpm_limit" placeholder="RPM，0 不限"><input name="tpm_limit" placeholder="TPM，0 不限"><input name="concurrent_limit" placeholder="并发，0 不限"><input name="daily_budget_cents" placeholder="日预算 cents"><input name="monthly_budget_cents" placeholder="月预算 cents"><button>创建 Key</button></form></div><table><tr><th>名称</th><th>前缀</th><th>状态</th><th>协议</th><th>模型</th><th>RPM</th><th>TPM</th><th>并发</th><th>预算</th><th>创建时间</th></tr>{{range .Keys}}<tr><td>{{.Name}}</td><td>{{.Prefix}}</td><td>{{.Status}}</td><td>{{join .AllowedProtocols ","}}</td><td>{{join .AllowedModels ","}}</td><td>{{.RPMLimit}}</td><td>{{.TPMLimit}}</td><td>{{.ConcurrentLimit}}</td><td>{{.DailyBudgetCents}} / {{.MonthlyBudgetCents}}</td><td>{{.CreatedAt}}</td></tr>{{end}}</table>{{template "layout-end" .}}{{end}}
{{define "providers"}}{{template "layout-start" .}}<div class="card"><form method="post" class="formgrid"><input name="id" placeholder="provider id"><input name="name" placeholder="名称"><input name="base_url" placeholder="https://.../v1"><select name="protocol"><option value="openai">openai</option><option value="anthropic">anthropic</option></select><input name="master_key" placeholder="上游 master key"><input name="default_proxy_id" placeholder="direct"><input name="timeout_seconds" value="90"><button>保存 Provider</button></form></div><table><tr><th>ID</th><th>名称</th><th>Base URL</th><th>协议</th><th>代理</th><th>状态</th></tr>{{range .Providers}}<tr><td>{{.ID}}</td><td>{{.Name}}</td><td>{{.BaseURL}}</td><td>{{.Protocol}}</td><td>{{.DefaultProxyID}}</td><td>{{.Status}}</td></tr>{{end}}</table>{{template "layout-end" .}}{{end}}
{{define "models"}}{{template "layout-start" .}}<div class="card"><form method="post" class="formgrid"><input name="public_name" placeholder="public model"><input name="provider_id" placeholder="provider id"><input name="upstream_model" placeholder="upstream model"><select name="protocol"><option value="openai">openai</option><option value="anthropic">anthropic</option></select><input name="proxy_profile_id" placeholder="direct"><input name="context_tokens" placeholder="上下文长度"><button>保存模型</button></form></div><table><tr><th>Public</th><th>Provider</th><th>Upstream</th><th>协议</th><th>代理</th><th>状态</th></tr>{{range .Routes}}<tr><td>{{.PublicName}}</td><td>{{.ProviderID}}</td><td>{{.UpstreamModel}}</td><td>{{.Protocol}}</td><td>{{.ProxyProfileID}}</td><td>{{.Enabled}}</td></tr>{{end}}</table>{{template "layout-end" .}}{{end}}
{{define "proxies"}}{{template "layout-start" .}}<div class="card"><form method="post" class="formgrid"><input name="id" placeholder="direct"><input name="name" placeholder="名称"><select name="type"><option value="direct">direct</option><option value="http">http</option><option value="https">https</option><option value="socks5">socks5</option><option value="xray">xray</option><option value="v2ray">v2ray</option></select><input name="endpoint" placeholder="host:port 或 URL"><input name="auth" placeholder="user:password"><input name="region" placeholder="hk"><input name="timeout_seconds" value="60"><button>保存代理</button></form></div><table><tr><th>ID</th><th>名称</th><th>类型</th><th>端点</th><th>地区</th><th>状态</th></tr>{{range .Profiles}}<tr><td>{{.ID}}</td><td>{{.Name}}</td><td>{{.Type}}</td><td>{{.Endpoint}}</td><td>{{.Region}}</td><td>{{.Status}}</td></tr>{{end}}</table>{{template "layout-end" .}}{{end}}
{{define "placeholder"}}{{template "layout-start" .}}<div class="card">该页面已挂载，后续可在当前服务端渲染框架内继续扩展表单和报表。</div>{{template "layout-end" .}}{{end}}
`
