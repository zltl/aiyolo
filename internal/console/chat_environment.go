package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatEnvironmentLocal            = "local"
	consoleChatEnvironmentCloudAgentPrefix = "cloud-agent:"
)

var consoleChatPreferredImageModels = []string{
	"openai/gpt-image-2",
	"black-forest-labs/flux-1.1-pro-ultra",
}

type consoleChatEnvironmentOption struct {
	Value string
	Label string
}

type consoleChatEnvironmentEnsureResponse struct {
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Error         string `json:"error,omitempty"`
}

func consoleChatEnvironmentValue(workerID string) string {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return consoleChatEnvironmentLocal
	}
	return consoleChatEnvironmentCloudAgentPrefix + workerID
}

func consoleChatEnvironmentWorkerID(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, consoleChatEnvironmentCloudAgentPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, consoleChatEnvironmentCloudAgentPrefix))
}

func normalizeConsoleChatEnvironmentValue(raw string, options []consoleChatEnvironmentOption) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return consoleChatEnvironmentLocal
	}
	for _, option := range options {
		if option.Value == raw {
			return raw
		}
	}
	return consoleChatEnvironmentLocal
}

func consoleChatEnvironmentLabel(value string, options []consoleChatEnvironmentOption) string {
	selected := normalizeConsoleChatEnvironmentValue(value, options)
	for _, option := range options {
		if option.Value == selected {
			return option.Label
		}
	}
	if selected == "" {
		return consoleChatEnvironmentLocal
	}
	return selected
}

func consoleChatEnvironmentToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var builder strings.Builder
	lastDash := false
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '-', ch == '_':
			builder.WriteRune(ch)
			lastDash = false
		default:
			if lastDash || builder.Len() == 0 {
				continue
			}
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-_")
	if result == "" {
		return "default"
	}
	return result
}

func consoleChatCloudAgentAccountID(workerID string) string {
	return "cloud-agent-" + consoleChatEnvironmentToken(workerID)
}

func consoleChatCloudAgentSessionID(chatSessionID string) string {
	return "chat-env-" + consoleChatEnvironmentToken(chatSessionID)
}

func consoleChatHostIsLoopback(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && (parsed.IsLoopback() || parsed.IsUnspecified())
}

func consoleChatCloudAgentBaseURL(baseURL string, worker domain.WorkerServer) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	if !consoleChatHostIsLoopback(parsed.Hostname()) {
		return baseURL
	}
	reachableHost := strings.TrimSpace(worker.SSHHost)
	if consoleChatHostIsLoopback(reachableHost) || reachableHost == "" {
		reachableHost = "host.docker.internal"
	}
	if port := parsed.Port(); port != "" {
		parsed.Host = net.JoinHostPort(reachableHost, port)
	} else {
		parsed.Host = reachableHost
	}
	return strings.TrimRight(parsed.String(), "/")
}

func (handler *Handler) consoleChatCloudAgentASSArtifactURLs(baseURL string) (string, string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if handler == nil || baseURL == "" || !handler.cfg.Artifacts.Enabled() {
		return "", ""
	}
	objectKey := workerops.CloudAgentASSArtifactObjectKey
	return baseURL + handler.cfg.Artifacts.ProxyObjectURL(objectKey), baseURL + handler.cfg.Artifacts.ProxyObjectURL(objectKey+".sha256")
}

func (handler *Handler) chatEnvironmentOptions(ctx context.Context, r *http.Request) ([]consoleChatEnvironmentOption, error) {
	workers, err := handler.store.ListWorkerServers(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].Name != workers[j].Name {
			return workers[i].Name < workers[j].Name
		}
		return workers[i].ID < workers[j].ID
	})
	options := []consoleChatEnvironmentOption{{
		Value: consoleChatEnvironmentLocal,
		Label: handler.requestText(r, "本地", "Local"),
	}}
	for _, worker := range workers {
		workerID := strings.TrimSpace(worker.ID)
		if workerID == "" {
			continue
		}
		name := strings.TrimSpace(worker.Name)
		label := workerID
		if name != "" && name != workerID {
			label = name + " (" + workerID + ")"
		}
		options = append(options, consoleChatEnvironmentOption{
			Value: consoleChatEnvironmentValue(workerID),
			Label: handler.requestText(r, "Cloud Agent · ", "Cloud agent · ") + label,
		})
	}
	return options, nil
}

func (handler *Handler) restoreConsoleChatEnvironment(ctx context.Context, userID string, chatSessionID string) string {
	chatSessionID = strings.TrimSpace(chatSessionID)
	if chatSessionID == "" {
		return consoleChatEnvironmentLocal
	}
	session, err := handler.store.GetCloudAgentSession(ctx, strings.TrimSpace(userID), consoleChatCloudAgentSessionID(chatSessionID))
	if err != nil {
		return consoleChatEnvironmentLocal
	}
	if strings.TrimSpace(session.Status) != domain.CloudAgentSessionStatusActive {
		return consoleChatEnvironmentLocal
	}
	return consoleChatEnvironmentValue(session.WorkerID)
}

func consoleChatRoutePublicNames(routes []consoleChatRouteView) []string {
	seen := make(map[string]struct{}, len(routes))
	result := make([]string, 0, len(routes))
	for _, route := range routes {
		name := strings.TrimSpace(route.PublicName)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func consoleChatAllowedModelAliases(model string) []string {
	aliases := []string{model}
	if slot, ok := consoleChatAllowedModelSlot(model); ok && slot == "gpt-image-2" {
		aliases = append(aliases, "gpt-image-2", "chatgpt-image-2", "openai/gpt-image-2")
	}
	return aliases
}

func consoleChatExpandAllowedModels(models []string) []string {
	models = append(models, consoleChatPreferredImageModels...)
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}
		for _, alias := range consoleChatAllowedModelAliases(trimmed) {
			candidate := strings.TrimSpace(alias)
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			result = append(result, candidate)
		}
	}
	return result
}

func consoleChatStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}
	return set
}

func consoleChatSameStringSet(left, right []string) bool {
	leftSet := consoleChatStringSet(left)
	rightSet := consoleChatStringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if _, ok := rightSet[value]; !ok {
			return false
		}
	}
	return true
}

func consoleChatCloudAgentAllowedProtocols() []string {
	return []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}
}

func (handler *Handler) ensureConsoleChatEnvironmentAPIKey(ctx context.Context, userID, workerID string, account domain.CloudAgentAccount, allowedModels []string, now time.Time) (domain.CloudAgentAccount, error) {
	desiredProtocols := consoleChatCloudAgentAllowedProtocols()
	var existingKey domain.APIKey
	if credential := strings.TrimSpace(account.Credential); credential != "" {
		apiKey, err := handler.store.FindAPIKeyByHash(ctx, auth.HashAPIKey(credential))
		switch {
		case err == nil:
			if auth.APIKeyActive(apiKey, now) && consoleChatSameStringSet(apiKey.AllowedProtocols, desiredProtocols) && consoleChatSameStringSet(apiKey.AllowedModels, allowedModels) {
				return account, nil
			}
			existingKey = apiKey
		case errors.Is(err, storage.ErrNotFound):
		default:
			return domain.CloudAgentAccount{}, err
		}
	}
	expiresAt := existingKey.ExpiresAt
	if expiresAt != nil && !expiresAt.After(now) {
		expiresAt = nil
	}
	clearKey, apiKey, err := newConsoleAPIKey(apiKeySpec{
		ID:                 strings.TrimSpace(existingKey.ID),
		Name:               firstNonEmpty(strings.TrimSpace(existingKey.Name), fmt.Sprintf("Cloud Agent %s %s", workerID, now.Format("2006-01-02 15:04:05"))),
		Kind:               "live",
		UserID:             firstNonEmpty(strings.TrimSpace(existingKey.UserID), userID),
		OrganizationID:     strings.TrimSpace(existingKey.OrganizationID),
		ProjectID:          strings.TrimSpace(existingKey.ProjectID),
		Status:             domain.StatusActive,
		AllowedProtocols:   desiredProtocols,
		AllowedModels:      allowedModels,
		RPMLimit:           existingKey.RPMLimit,
		TPMLimit:           existingKey.TPMLimit,
		ConcurrentLimit:    existingKey.ConcurrentLimit,
		DailyBudgetCents:   existingKey.DailyBudgetCents,
		MonthlyBudgetCents: existingKey.MonthlyBudgetCents,
		ExpiresAt:          expiresAt,
		CreatedAt:          existingKey.CreatedAt,
	})
	if err != nil {
		return domain.CloudAgentAccount{}, err
	}
	if err := handler.store.CreateAPIKey(ctx, apiKey); err != nil {
		return domain.CloudAgentAccount{}, err
	}
	account.Credential = clearKey
	return account, nil
}

func (handler *Handler) closeConsoleChatEnvironmentSession(ctx context.Context, userID string, chatSessionID string) error {
	chatSessionID = strings.TrimSpace(chatSessionID)
	if chatSessionID == "" {
		return nil
	}
	session, err := handler.store.GetCloudAgentSession(ctx, strings.TrimSpace(userID), consoleChatCloudAgentSessionID(chatSessionID))
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	session.Status = domain.CloudAgentSessionStatusClosed
	session.ClosedAt = &now
	session.LastError = ""
	return handler.store.UpsertCloudAgentSession(ctx, session)
}

func consoleChatCloudAgentReusable(account domain.CloudAgentAccount, workerID string, publicName string, now time.Time) bool {
	if strings.TrimSpace(account.WorkerID) != strings.TrimSpace(workerID) {
		return false
	}
	if strings.TrimSpace(account.Status) != domain.CloudAgentStatusRunning {
		return false
	}
	if strings.TrimSpace(account.ContainerName) == "" || strings.TrimSpace(account.LastError) != "" {
		return false
	}
	if currentModel := strings.TrimSpace(account.ModelPublicName); currentModel != "" && strings.TrimSpace(publicName) != "" && currentModel != strings.TrimSpace(publicName) {
		return false
	}
	return true
}

func (handler *Handler) reusableConsoleChatCloudAgentEnvironment(ctx context.Context, userID string, chatSessionID string, workerID string, publicName string, account domain.CloudAgentAccount, now time.Time) (domain.CloudAgentAccount, domain.CloudAgentSession, bool, error) {
	if !consoleChatCloudAgentReusable(account, workerID, publicName, now) {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, false, nil
	}
	cloudSession, err := handler.store.GetCloudAgentSession(ctx, strings.TrimSpace(userID), consoleChatCloudAgentSessionID(chatSessionID))
	if errors.Is(err, storage.ErrNotFound) {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, false, nil
	}
	if err != nil {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, false, err
	}
	if strings.TrimSpace(cloudSession.Status) != domain.CloudAgentSessionStatusActive ||
		strings.TrimSpace(cloudSession.WorkerID) != strings.TrimSpace(workerID) ||
		strings.TrimSpace(cloudSession.AccountID) != strings.TrimSpace(account.ID) {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, false, nil
	}
	account.LastSeenAt = &now
	if err := handler.store.UpsertCloudAgentAccount(ctx, account); err != nil {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, false, err
	}
	return account, cloudSession, true, nil
}

func (handler *Handler) ensureConsoleChatEnvironment(ctx context.Context, r *http.Request, state *consoleChatPageState) (consoleChatEnvironmentEnsureResponse, error) {
	if state == nil {
		return consoleChatEnvironmentEnsureResponse{}, fmt.Errorf("chat state is required")
	}
	state.Form.Environment = normalizeConsoleChatEnvironmentValue(state.Form.Environment, state.EnvironmentOptions)
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	if state.Form.Environment == consoleChatEnvironmentLocal {
		if err := handler.closeConsoleChatEnvironmentSession(ctx, userID, state.Form.ClientSessionID); err != nil {
			return consoleChatEnvironmentEnsureResponse{}, err
		}
		return consoleChatEnvironmentEnsureResponse{
			Status:      "local",
			SessionID:   strings.TrimSpace(state.Form.ClientSessionID),
			Environment: consoleChatEnvironmentLocal,
			Notice:      handler.requestText(r, "已切回本地环境", "Switched back to the local environment"),
		}, nil
	}
	workerID := consoleChatEnvironmentWorkerID(state.Form.Environment)
	if workerID == "" {
		return consoleChatEnvironmentEnsureResponse{}, errors.New(handler.requestText(r, "无效的 Chat 环境选择", "Invalid chat environment selection"))
	}
	if strings.TrimSpace(state.Form.ClientSessionID) == "" {
		state.Form.ClientSessionID = newConsoleID("chat")
	}
	worker, proxy, key, err := handler.workerExecutionInputs(ctx, workerID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return consoleChatEnvironmentEnsureResponse{}, errors.New(handler.requestText(r, "选中的 Worker 不存在或缺少 SSH 配置", "The selected worker is missing or does not have a usable SSH configuration"))
		}
		return consoleChatEnvironmentEnsureResponse{}, err
	}
	baseURL := consoleChatCloudAgentBaseURL(handler.codexPublicBaseURL(r), worker)
	if strings.TrimSpace(baseURL) == "" {
		return consoleChatEnvironmentEnsureResponse{}, errors.New(handler.requestText(r, "无法解析当前 AIYolo 访问地址", "Unable to resolve the current AIYolo public URL"))
	}
	allowedModels := consoleChatExpandAllowedModels(consoleChatRoutePublicNames(state.Routes))
	assDownloadURL, assSHA256URL := handler.consoleChatCloudAgentASSArtifactURLs(baseURL)
	accountID := consoleChatCloudAgentAccountID(workerID)
	now := time.Now().UTC()
	account, err := handler.store.GetCloudAgentAccount(ctx, userID, accountID)
	if errors.Is(err, storage.ErrNotFound) {
		account = domain.CloudAgentAccount{
			ID:            accountID,
			UserID:        userID,
			WorkerID:      workerID,
			AgentType:     domain.CloudAgentTypeCodex,
			WorkspacePath: domain.DefaultCloudAgentWorkspacePath,
			CreatedAt:     now,
		}
	} else if err != nil {
		return consoleChatEnvironmentEnsureResponse{}, err
	}
	account.WorkerID = workerID
	account, err = handler.ensureConsoleChatEnvironmentAPIKey(ctx, userID, workerID, account, allowedModels, now)
	if err != nil {
		return consoleChatEnvironmentEnsureResponse{}, err
	}
	if account, cloudSession, ok, err := handler.reusableConsoleChatCloudAgentEnvironment(ctx, userID, state.Form.ClientSessionID, workerID, state.Form.PublicName, account, now); err != nil {
		return consoleChatEnvironmentEnsureResponse{}, err
	} else if ok {
		return consoleChatEnvironmentEnsureResponse{
			Status:        "ready",
			SessionID:     state.Form.ClientSessionID,
			Environment:   state.Form.Environment,
			WorkerID:      workerID,
			ContainerName: account.ContainerName,
			WorkspacePath: firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath)),
			Notice:        handler.requestText(r, "Cloud Agent 已在 "+workerID+" 就绪", "Cloud agent is ready on "+workerID),
		}, nil
	}
	account.AgentType = domain.CloudAgentTypeCodex
	account.ModelPublicName = firstNonEmpty(strings.TrimSpace(state.Form.PublicName), strings.TrimSpace(account.ModelPublicName))
	account.WorkspacePath = firstNonEmpty(strings.TrimSpace(account.WorkspacePath), domain.DefaultCloudAgentWorkspacePath)
	account.Status = domain.CloudAgentStatusStarting
	account.LastError = ""
	if err := handler.store.UpsertCloudAgentAccount(ctx, account); err != nil {
		return consoleChatEnvironmentEnsureResponse{}, err
	}
	instance, err := handler.ensureCloudAgent(ctx, worker, key, proxy, workerops.CloudAgentStartOptions{
		UserID:         userID,
		AgentType:      account.AgentType,
		ContainerName:  strings.TrimSpace(account.ContainerName),
		WorkspacePath:  account.WorkspacePath,
		APIBaseURL:     strings.TrimRight(baseURL, "/") + "/v1",
		ConsoleBaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:         account.Credential,
		DefaultModel:   account.ModelPublicName,
		AllowedModels:  allowedModels,
		OpenURL:        strings.TrimRight(baseURL, "/") + "/console/chat?session=" + url.QueryEscape(state.Form.ClientSessionID),
		ASSDownloadURL: assDownloadURL,
		ASSSHA256URL:   assSHA256URL,
	})
	if err != nil {
		account.Status = domain.CloudAgentStatusError
		account.LastError = err.Error()
		_ = handler.store.UpsertCloudAgentAccount(context.WithoutCancel(ctx), account)
		_ = handler.store.UpsertCloudAgentSession(context.WithoutCancel(ctx), domain.CloudAgentSession{
			ID:            consoleChatCloudAgentSessionID(state.Form.ClientSessionID),
			UserID:        userID,
			WorkerID:      workerID,
			AccountID:     account.ID,
			AgentType:     account.AgentType,
			ChatSessionID: state.Form.ClientSessionID,
			WorkspacePath: account.WorkspacePath,
			Status:        domain.CloudAgentSessionStatusPending,
			LastError:     err.Error(),
		})
		return consoleChatEnvironmentEnsureResponse{}, errors.New(handler.requestText(r, "Cloud Agent 启动失败：", "Cloud agent startup failed: ") + err.Error())
	}
	now = time.Now().UTC()
	account.ContainerID = strings.TrimSpace(instance.ContainerID)
	account.ContainerName = firstNonEmpty(strings.TrimSpace(instance.ContainerName), strings.TrimSpace(account.ContainerName))
	account.Status = domain.CloudAgentStatusRunning
	account.LastError = ""
	account.LastStartedAt = &now
	account.LastSeenAt = &now
	if err := handler.store.UpsertCloudAgentAccount(ctx, account); err != nil {
		return consoleChatEnvironmentEnsureResponse{}, err
	}
	if err := handler.store.UpsertCloudAgentSession(ctx, domain.CloudAgentSession{
		ID:            consoleChatCloudAgentSessionID(state.Form.ClientSessionID),
		UserID:        userID,
		WorkerID:      workerID,
		AccountID:     account.ID,
		AgentType:     account.AgentType,
		ChatSessionID: state.Form.ClientSessionID,
		WorkspacePath: account.WorkspacePath,
		Status:        domain.CloudAgentSessionStatusActive,
		LastError:     "",
	}); err != nil {
		return consoleChatEnvironmentEnsureResponse{}, err
	}
	return consoleChatEnvironmentEnsureResponse{
		Status:        "ready",
		SessionID:     state.Form.ClientSessionID,
		Environment:   state.Form.Environment,
		WorkerID:      workerID,
		ContainerName: account.ContainerName,
		WorkspacePath: account.WorkspacePath,
		Notice:        handler.requestText(r, "Cloud Agent 已在 "+workerID+" 就绪", "Cloud agent is ready on "+workerID),
	}, nil
}

func (handler *Handler) chatEnvironmentEnsure(w http.ResponseWriter, r *http.Request) {
	if err := parseConsoleChatForm(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state, err := handler.chatPageState(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response, err := handler.ensureConsoleChatEnvironment(r.Context(), r, &state)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		} else if strings.TrimSpace(state.Form.Environment) == "" || strings.Contains(strings.ToLower(err.Error()), "invalid") || strings.Contains(strings.ToLower(err.Error()), "unable to resolve") {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		response.Status = "error"
		response.SessionID = strings.TrimSpace(state.Form.ClientSessionID)
		response.Environment = normalizeConsoleChatEnvironmentValue(state.Form.Environment, state.EnvironmentOptions)
		response.Error = err.Error()
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	_ = json.NewEncoder(w).Encode(response)
}
