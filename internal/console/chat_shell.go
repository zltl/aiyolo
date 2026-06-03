package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatShellPagePath   = "/console/chat/shell"
	consoleChatShellSocketPath = "/console/chat/shell/ws"
	consoleChatShellStatePath  = "/console/chat/shell/state"
	consoleChatShellDefaultID  = "default"
	consoleChatShellCols       = 120
	consoleChatShellRows       = 32
	consoleChatShellStateLimit = 8
)

type consoleChatShellPageState struct {
	SessionID     string
	TerminalID    string
	WorkerID      string
	ContainerName string
	WorkspacePath string
	SocketURL     string
	ChatURL       string
	ShellState    consoleChatShellState
	Error         string
}

type consoleChatShellSocketRequest struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type consoleChatShellSocketEvent struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
}

type consoleChatShellReadyResponse struct {
	Status        string                `json:"status"`
	SessionID     string                `json:"sessionId,omitempty"`
	TerminalID    string                `json:"terminalId,omitempty"`
	Environment   string                `json:"environment,omitempty"`
	WorkerID      string                `json:"workerId,omitempty"`
	ContainerName string                `json:"containerName,omitempty"`
	WorkspacePath string                `json:"workspacePath,omitempty"`
	SocketURL     string                `json:"socketUrl,omitempty"`
	ShellState    consoleChatShellState `json:"shellState,omitempty"`
	Error         string                `json:"error,omitempty"`
}

type consoleChatShellStateResponse struct {
	Status        string                `json:"status"`
	SessionID     string                `json:"sessionId,omitempty"`
	Environment   string                `json:"environment,omitempty"`
	WorkerID      string                `json:"workerId,omitempty"`
	ContainerName string                `json:"containerName,omitempty"`
	WorkspacePath string                `json:"workspacePath,omitempty"`
	ShellState    consoleChatShellState `json:"shellState"`
	Error         string                `json:"error,omitempty"`
}

type consoleChatShellStateRequest struct {
	SessionID        string                     `json:"sessionID"`
	ActiveTerminalID string                     `json:"activeTerminalID"`
	Instances        []consoleChatShellSnapshot `json:"instances"`
	Hidden           bool                       `json:"hidden"`
	UpdatedAt        string                     `json:"updatedAt,omitempty"`
}

type consoleChatShellState struct {
	ActiveTerminalID string                     `json:"activeTerminalID,omitempty"`
	Instances        []consoleChatShellSnapshot `json:"instances"`
	Hidden           bool                       `json:"hidden"`
	UpdatedAt        string                     `json:"updatedAt,omitempty"`
}

type consoleChatShellSnapshot struct {
	TerminalID              string                       `json:"terminalID"`
	Label                   string                       `json:"label,omitempty"`
	SessionID               string                       `json:"sessionID"`
	SocketURL               string                       `json:"socketURL,omitempty"`
	WorkerID                string                       `json:"workerID,omitempty"`
	ContainerName           string                       `json:"containerName,omitempty"`
	WorkspacePath           string                       `json:"workspacePath,omitempty"`
	CurrentWorkingDirectory string                       `json:"currentWorkingDirectory,omitempty"`
	Meta                    consoleChatShellSnapshotMeta `json:"meta,omitempty"`
}

type consoleChatShellSnapshotMeta struct {
	SessionID               string `json:"sessionID,omitempty"`
	WorkerID                string `json:"workerID,omitempty"`
	ContainerName           string `json:"containerName,omitempty"`
	WorkspacePath           string `json:"workspacePath,omitempty"`
	CurrentWorkingDirectory string `json:"currentWorkingDirectory,omitempty"`
}

func (state consoleChatShellPageState) data() map[string]any {
	return map[string]any{
		"Title":          "Cloud Agent Terminal",
		"ChatShell":      state,
		"ChatShellError": state.Error,
	}
}

func normalizeConsoleChatShellTerminalID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return consoleChatShellDefaultID
	}
	var builder strings.Builder
	for _, r := range trimmed {
		if r <= 32 || strings.ContainsRune("/?#&=\\", r) {
			continue
		}
		builder.WriteRune(r)
		if builder.Len() >= 80 {
			break
		}
	}
	if builder.Len() == 0 {
		return consoleChatShellDefaultID
	}
	return builder.String()
}

func consoleChatShellTerminalID(r *http.Request) string {
	return normalizeConsoleChatShellTerminalID(r.URL.Query().Get("terminal"))
}

func normalizeConsoleChatOptionalShellTerminalID(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return normalizeConsoleChatShellTerminalID(value)
}

func normalizeConsoleChatShellWorkingDirectory(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsAny(trimmed, "\x00\r\n") || !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	return path.Clean(trimmed)
}

func normalizeConsoleChatShellLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range trimmed {
		if r == 0 || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		builder.WriteRune(r)
		if builder.Len() >= 80 {
			break
		}
	}
	return strings.TrimSpace(builder.String())
}

func consoleChatShellSocketURL(chatSessionID, terminalID string) string {
	query := url.Values{}
	query.Set("session", strings.TrimSpace(chatSessionID))
	query.Set("terminal", normalizeConsoleChatShellTerminalID(terminalID))
	return consoleChatShellSocketPath + "?" + query.Encode()
}

func consoleChatShellStateFromSession(cloudSession domain.CloudAgentSession, chatSessionID string, workerID string, containerName string, workspacePath string) consoleChatShellState {
	var state consoleChatShellState
	if raw := strings.TrimSpace(cloudSession.ShellStateJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &state)
	}
	return normalizeConsoleChatShellState(state, chatSessionID, workerID, containerName, workspacePath)
}

func normalizeConsoleChatShellState(state consoleChatShellState, chatSessionID string, workerID string, containerName string, workspacePath string) consoleChatShellState {
	chatSessionID = strings.TrimSpace(chatSessionID)
	workerID = strings.TrimSpace(workerID)
	containerName = strings.TrimSpace(containerName)
	workspacePath = strings.TrimSpace(workspacePath)
	seen := make(map[string]struct{}, len(state.Instances))
	instances := make([]consoleChatShellSnapshot, 0, len(state.Instances))
	for _, snapshot := range state.Instances {
		terminalID := normalizeConsoleChatShellTerminalID(snapshot.TerminalID)
		if _, ok := seen[terminalID]; ok {
			continue
		}
		seen[terminalID] = struct{}{}
		currentWorkingDirectory := normalizeConsoleChatShellWorkingDirectory(firstNonEmpty(snapshot.CurrentWorkingDirectory, snapshot.Meta.CurrentWorkingDirectory))
		if currentWorkingDirectory == "" {
			currentWorkingDirectory = normalizeConsoleChatShellWorkingDirectory(workspacePath)
		}
		instances = append(instances, consoleChatShellSnapshot{
			TerminalID:              terminalID,
			Label:                   normalizeConsoleChatShellLabel(snapshot.Label),
			SessionID:               chatSessionID,
			SocketURL:               consoleChatShellSocketURL(chatSessionID, terminalID),
			WorkerID:                workerID,
			ContainerName:           containerName,
			WorkspacePath:           workspacePath,
			CurrentWorkingDirectory: currentWorkingDirectory,
			Meta: consoleChatShellSnapshotMeta{
				SessionID:               chatSessionID,
				WorkerID:                workerID,
				ContainerName:           containerName,
				WorkspacePath:           workspacePath,
				CurrentWorkingDirectory: currentWorkingDirectory,
			},
		})
		if len(instances) >= consoleChatShellStateLimit {
			break
		}
	}
	activeTerminalID := normalizeConsoleChatShellTerminalID(state.ActiveTerminalID)
	foundActive := false
	for _, snapshot := range instances {
		if snapshot.TerminalID == activeTerminalID {
			foundActive = true
			break
		}
	}
	if !foundActive {
		if len(instances) > 0 {
			activeTerminalID = instances[0].TerminalID
		} else {
			activeTerminalID = ""
		}
	}
	return consoleChatShellState{
		ActiveTerminalID: activeTerminalID,
		Instances:        instances,
		Hidden:           state.Hidden,
		UpdatedAt:        strings.TrimSpace(state.UpdatedAt),
	}
}

func consoleChatShellStatePayload(state consoleChatShellState) string {
	if len(state.Instances) == 0 {
		return ""
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return string(payload)
}

func (handler *Handler) resolveConsoleChatCloudAgentTarget(ctx context.Context, r *http.Request, chatSessionID string) (domain.WorkerServer, domain.WorkerSSHKey, domain.CloudAgentAccount, domain.CloudAgentSession, error) {
	return handler.resolveConsoleChatCloudAgentTargetWithRuntime(ctx, r, chatSessionID, false)
}

func (handler *Handler) resolveConsoleChatCloudAgentRuntimeTarget(ctx context.Context, r *http.Request, chatSessionID string) (domain.WorkerServer, domain.WorkerSSHKey, domain.CloudAgentAccount, domain.CloudAgentSession, error) {
	return handler.resolveConsoleChatCloudAgentTargetWithRuntime(ctx, r, chatSessionID, true)
}

func (handler *Handler) resolveConsoleChatCloudAgentTargetWithRuntime(ctx context.Context, r *http.Request, chatSessionID string, ensureRuntime bool) (domain.WorkerServer, domain.WorkerSSHKey, domain.CloudAgentAccount, domain.CloudAgentSession, error) {
	chatSessionID = strings.TrimSpace(chatSessionID)
	if chatSessionID == "" {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, errors.New(handler.requestText(r, "缺少 chat session，无法打开 shell。", "Missing chat session; unable to open the shell."))
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	cloudSession, err := handler.store.GetCloudAgentSession(ctx, userID, consoleChatCloudAgentSessionID(chatSessionID))
	if errors.Is(err, storage.ErrNotFound) {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, fmt.Errorf("%w: %s", storage.ErrNotFound, handler.requestText(r, "当前会话没有可用的 Cloud Agent。", "The current chat session does not have an active cloud agent."))
	}
	if err != nil {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	if strings.TrimSpace(cloudSession.Status) != domain.CloudAgentSessionStatusActive {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, errors.New(handler.requestText(r, "当前 Cloud Agent 会话未激活，请先在 chat 页面重新启动环境。", "The cloud agent session is not active. Restart the environment from the chat page first."))
	}
	account, err := handler.store.GetCloudAgentAccount(ctx, userID, cloudSession.AccountID)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, fmt.Errorf("%w: %s", storage.ErrNotFound, handler.requestText(r, "找不到当前 Cloud Agent 容器记录。", "The cloud agent container record could not be found."))
	}
	if err != nil {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	if account.WorkerID != cloudSession.WorkerID {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, errors.New(handler.requestText(r, "Cloud Agent 记录与 Worker 不匹配。", "The cloud agent record does not match the worker."))
	}
	if strings.TrimSpace(account.ContainerName) == "" {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, errors.New(handler.requestText(r, "Cloud Agent 容器尚未就绪，请先在 chat 页面重试环境启动。", "The cloud agent container is not ready yet. Retry environment startup from the chat page first."))
	}
	worker, proxy, key, err := handler.workerExecutionInputs(ctx, cloudSession.WorkerID)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, fmt.Errorf("%w: %s", storage.ErrNotFound, handler.requestText(r, "Worker SSH 配置不存在，无法连接 shell。", "The worker SSH configuration is missing, so the shell cannot be opened."))
	}
	if err != nil {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	account.WorkspacePath = firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath))
	if ensureRuntime {
		account, cloudSession, err = handler.ensureConsoleChatCloudAgentRuntime(ctx, r, userID, worker, key, proxy, account, cloudSession)
		if err != nil {
			return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
		}
	}
	return worker, key, account, cloudSession, nil
}

func (handler *Handler) consoleChatAllowedModelPublicNames(ctx context.Context) ([]string, error) {
	routes, err := handler.store.ListModelRoutes(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := handler.store.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	return consoleChatRoutePublicNames(consoleChatRoutes(routes, providers)), nil
}

func (handler *Handler) ensureConsoleChatCloudAgentRuntime(ctx context.Context, r *http.Request, userID string, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession) (domain.CloudAgentAccount, domain.CloudAgentSession, error) {
	baseURL := consoleChatCloudAgentBaseURL(handler.codexPublicBaseURL(r), worker)
	if strings.TrimSpace(baseURL) == "" {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, errors.New(handler.requestText(r, "无法解析当前 AIYolo 访问地址", "Unable to resolve the current AIYolo public URL"))
	}
	allowedModels, err := handler.consoleChatAllowedModelPublicNames(ctx)
	if err != nil {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	allowedModels = consoleChatExpandAllowedModels(allowedModels)
	assDownloadURL, assSHA256URL := handler.consoleChatCloudAgentASSArtifactURLs(baseURL)
	now := time.Now().UTC()
	account, err = handler.ensureConsoleChatEnvironmentAPIKey(ctx, userID, worker.ID, account, allowedModels, now)
	if err != nil {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	account.WorkerID = worker.ID
	account.AgentType = domain.CloudAgentTypeCodex
	account.WorkspacePath = firstNonEmpty(strings.TrimSpace(account.WorkspacePath), strings.TrimSpace(cloudSession.WorkspacePath), domain.DefaultCloudAgentWorkspacePath)
	account.Status = domain.CloudAgentStatusStarting
	account.LastError = ""
	if err := handler.store.UpsertCloudAgentAccount(ctx, account); err != nil {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}

	chatSessionID := firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(strings.TrimPrefix(cloudSession.ID, "chat-env-")))
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
		OpenURL:        strings.TrimRight(baseURL, "/") + "/console/chat?session=" + url.QueryEscape(chatSessionID),
		ASSDownloadURL: assDownloadURL,
		ASSSHA256URL:   assSHA256URL,
	})
	if err != nil {
		account.Status = domain.CloudAgentStatusError
		account.LastError = err.Error()
		_ = handler.store.UpsertCloudAgentAccount(context.WithoutCancel(ctx), account)
		cloudSession.Status = domain.CloudAgentSessionStatusPending
		cloudSession.LastError = err.Error()
		_ = handler.store.UpsertCloudAgentSession(context.WithoutCancel(ctx), cloudSession)
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, errors.New(handler.requestText(r, "Cloud Agent 启动失败：", "Cloud agent startup failed: ") + err.Error())
	}
	now = time.Now().UTC()
	account.ContainerID = strings.TrimSpace(instance.ContainerID)
	account.ContainerName = firstNonEmpty(strings.TrimSpace(instance.ContainerName), strings.TrimSpace(account.ContainerName))
	account.Status = domain.CloudAgentStatusRunning
	account.LastError = ""
	account.LastStartedAt = &now
	account.LastSeenAt = &now
	if err := handler.store.UpsertCloudAgentAccount(ctx, account); err != nil {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	cloudSession.WorkerID = worker.ID
	cloudSession.AccountID = account.ID
	cloudSession.AgentType = account.AgentType
	cloudSession.ChatSessionID = chatSessionID
	cloudSession.WorkspacePath = account.WorkspacePath
	cloudSession.Status = domain.CloudAgentSessionStatusActive
	cloudSession.LastError = ""
	cloudSession.ClosedAt = nil
	if err := handler.store.UpsertCloudAgentSession(ctx, cloudSession); err != nil {
		return domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	return account, cloudSession, nil
}

func (handler *Handler) resolveConsoleChatShellTarget(ctx context.Context, r *http.Request) (domain.WorkerServer, domain.WorkerSSHKey, domain.CloudAgentAccount, domain.CloudAgentSession, error) {
	return handler.resolveConsoleChatCloudAgentRuntimeTarget(ctx, r, r.URL.Query().Get("session"))
}

func (handler *Handler) chatShellPageStateForSession(ctx context.Context, r *http.Request, chatSessionID string) (consoleChatShellPageState, error) {
	worker, _, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(ctx, r, chatSessionID)
	if err != nil {
		return consoleChatShellPageState{}, err
	}
	chatSessionID = firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(chatSessionID))
	terminalID := consoleChatShellTerminalID(r)
	workspacePath := firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath), domain.DefaultCloudAgentWorkspacePath)
	return consoleChatShellPageState{
		SessionID:     chatSessionID,
		TerminalID:    terminalID,
		WorkerID:      worker.ID,
		ContainerName: account.ContainerName,
		WorkspacePath: workspacePath,
		SocketURL:     consoleChatShellSocketURL(chatSessionID, terminalID),
		ChatURL:       consoleChatEndpoint + "?session=" + url.QueryEscape(chatSessionID),
		ShellState:    consoleChatShellStateFromSession(cloudSession, chatSessionID, worker.ID, account.ContainerName, workspacePath),
	}, nil
}

func (handler *Handler) chatShellPageState(ctx context.Context, r *http.Request) (consoleChatShellPageState, error) {
	return handler.chatShellPageStateForSession(ctx, r, r.URL.Query().Get("session"))
}

func (handler *Handler) chatShellPage(w http.ResponseWriter, r *http.Request) {
	state, err := handler.chatShellPageState(r.Context(), r)
	if err != nil {
		status := consoleChatShellErrorStatus(err)
		w.WriteHeader(status)
		handler.render(w, r, "chat-shell", consoleChatShellPageState{Error: err.Error()}.data())
		return
	}
	handler.render(w, r, "chat-shell", state.data())
}

func (handler *Handler) chatShellReady(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	state, err := handler.chatShellPageStateForSession(r.Context(), r, sessionID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatShellErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatShellReadyResponse{
			Status:     "error",
			SessionID:  sessionID,
			TerminalID: consoleChatShellTerminalID(r),
			Error:      err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatShellReadyResponse{
		Status:        "ready",
		SessionID:     state.SessionID,
		TerminalID:    state.TerminalID,
		Environment:   consoleChatEnvironmentValue(state.WorkerID),
		WorkerID:      state.WorkerID,
		ContainerName: state.ContainerName,
		WorkspacePath: state.WorkspacePath,
		SocketURL:     state.SocketURL,
		ShellState:    state.ShellState,
	})
}

func (handler *Handler) chatShellState(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	state, err := handler.chatShellPageStateForSession(r.Context(), r, sessionID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatShellErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatShellStateResponse{
			Status:    "error",
			SessionID: sessionID,
			Error:     err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatShellStateResponse{
		Status:        "ready",
		SessionID:     state.SessionID,
		Environment:   consoleChatEnvironmentValue(state.WorkerID),
		WorkerID:      state.WorkerID,
		ContainerName: state.ContainerName,
		WorkspacePath: state.WorkspacePath,
		ShellState:    state.ShellState,
	})
}

func (handler *Handler) saveChatShellState(w http.ResponseWriter, r *http.Request) {
	var request consoleChatShellStateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&request); err != nil {
		http.Error(w, handler.requestText(r, "Shell 状态数据无效。", "Shell state payload is invalid."), http.StatusBadRequest)
		return
	}
	sessionID := firstNonEmpty(strings.TrimSpace(request.SessionID), strings.TrimSpace(r.URL.Query().Get("session")))
	worker, _, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatShellErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatShellStateResponse{
			Status:    "error",
			SessionID: sessionID,
			Error:     err.Error(),
		})
		return
	}
	chatSessionID := firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(sessionID), strings.TrimSpace(strings.TrimPrefix(cloudSession.ID, "chat-env-")))
	workspacePath := firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath), domain.DefaultCloudAgentWorkspacePath)
	nextState := normalizeConsoleChatShellState(consoleChatShellState{
		ActiveTerminalID: request.ActiveTerminalID,
		Instances:        request.Instances,
		Hidden:           request.Hidden,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}, chatSessionID, worker.ID, account.ContainerName, workspacePath)
	cloudSession.ShellStateJSON = consoleChatShellStatePayload(nextState)
	if err := handler.store.UpsertCloudAgentSession(r.Context(), cloudSession); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(consoleChatShellStateResponse{Status: "error", SessionID: chatSessionID, Error: err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatShellStateResponse{
		Status:        "ready",
		SessionID:     chatSessionID,
		Environment:   consoleChatEnvironmentValue(worker.ID),
		WorkerID:      worker.ID,
		ContainerName: account.ContainerName,
		WorkspacePath: workspacePath,
		ShellState:    nextState,
	})
}

func (handler *Handler) chatShellSocket(w http.ResponseWriter, r *http.Request) {
	worker, key, account, cloudSession, err := handler.resolveConsoleChatShellTarget(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), consoleChatShellErrorStatus(err))
		return
	}
	server := websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: websocket.Handler(func(ws *websocket.Conn) {
			handler.serveChatShellSocket(ws, r, worker, key, account, cloudSession)
		}),
	}
	server.ServeHTTP(w, r)
}

func (handler *Handler) serveChatShellSocket(ws *websocket.Conn, r *http.Request, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession) {
	defer ws.Close()
	if handler.chatShells == nil {
		handler.chatShells = newConsoleChatShellRegistry()
	}
	chatSessionID := firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(strings.TrimPrefix(cloudSession.ID, "chat-env-")))
	terminalID := consoleChatShellTerminalID(r)
	registryKey := consoleChatShellRegistryKey(currentConsoleSessionSubject(r, handler.cfg.SecretKey), chatSessionID, terminalID)
	openCtx := context.WithoutCancel(r.Context())
	shellSession, err := handler.chatShells.getOrCreate(openCtx, registryKey, func(ctx context.Context) (workerops.InteractiveShell, error) {
		return handler.openCloudAgentShell(ctx, worker, key, account, cloudSession, consoleChatShellCols, consoleChatShellRows)
	})
	if err != nil {
		log.Printf("console chat shell open failed session_id=%s terminal_id=%s worker_id=%s container=%s err=%v", chatSessionID, terminalID, worker.ID, account.ContainerName, err)
		_ = websocket.JSON.Send(ws, consoleChatShellSocketEvent{Type: "error", Message: err.Error()})
		return
	}

	var sendMu sync.Mutex
	send := func(event consoleChatShellSocketEvent) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return websocket.JSON.Send(ws, event)
	}
	_ = send(consoleChatShellSocketEvent{Type: "ready", Message: handler.requestText(r, "终端已连接", "Terminal connected")})
	subscriberID, events, replay, done, closedMessage := shellSession.subscribe()
	defer shellSession.unsubscribe(subscriberID)
	if replay != "" {
		if err := send(consoleChatShellSocketEvent{Type: "output", Data: replay}); err != nil {
			return
		}
	}
	if done {
		_ = send(consoleChatShellSocketEvent{Type: "closed", Message: firstNonEmpty(closedMessage, handler.requestText(r, "终端已断开", "Terminal disconnected"))})
		return
	}

	eventDone := make(chan error, 1)
	stopEvents := make(chan struct{})
	go func() {
		for {
			select {
			case event, ok := <-events:
				if !ok {
					eventDone <- nil
					return
				}
				if err := send(event); err != nil {
					eventDone <- err
					return
				}
				if event.Type == "closed" {
					eventDone <- nil
					return
				}
			case <-stopEvents:
				eventDone <- nil
				return
			}
		}
	}()

	clientDone := make(chan error, 1)
	go func() {
		clientDone <- receiveConsoleChatShellInput(ws, shellSession, handler.requestText(r, "终端已关闭", "Terminal closed"))
	}()

	var finalErr error
	select {
	case finalErr = <-eventDone:
	case finalErr = <-clientDone:
	}
	close(stopEvents)
	if finalErr != nil && !consoleChatShellIgnorableError(finalErr) {
		log.Printf("console chat shell websocket disconnected session_id=%s terminal_id=%s worker_id=%s container=%s err=%v", chatSessionID, terminalID, worker.ID, account.ContainerName, finalErr)
	}
}

func receiveConsoleChatShellInput(ws *websocket.Conn, shell *consoleChatShellSession, closeMessage string) error {
	for {
		var payload consoleChatShellSocketRequest
		if err := websocket.JSON.Receive(ws, &payload); err != nil {
			return err
		}
		switch strings.TrimSpace(payload.Type) {
		case "input":
			if payload.Data == "" {
				continue
			}
			if _, err := io.WriteString(shell, payload.Data); err != nil {
				return err
			}
		case "resize":
			if err := shell.Resize(payload.Cols, payload.Rows); err != nil {
				return err
			}
		case "close":
			shell.close(closeMessage)
			return nil
		}
	}
}

func consoleChatShellErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "missing chat session") || strings.Contains(message, "unable to open the shell") {
		return http.StatusBadRequest
	}
	if strings.Contains(message, "not active") || strings.Contains(message, "not ready") || strings.Contains(message, "does not match") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func consoleChatShellIgnorableError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "closed network connection") || strings.Contains(message, "broken pipe") || strings.Contains(message, "connection reset by peer")
}
