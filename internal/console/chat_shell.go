package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/websocket"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatShellPagePath   = "/console/chat/shell"
	consoleChatShellSocketPath = "/console/chat/shell/ws"
	consoleChatShellCols       = 120
	consoleChatShellRows       = 32
)

type consoleChatShellPageState struct {
	SessionID     string
	WorkerID      string
	ContainerName string
	WorkspacePath string
	SocketURL     string
	ChatURL       string
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
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	SocketURL     string `json:"socketUrl,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (state consoleChatShellPageState) data() map[string]any {
	return map[string]any{
		"Title":          "Claude Code",
		"ChatShell":      state,
		"ChatShellError": state.Error,
	}
}

func (handler *Handler) resolveConsoleChatCloudAgentTarget(ctx context.Context, r *http.Request, chatSessionID string) (domain.WorkerServer, domain.WorkerSSHKey, domain.CloudAgentAccount, domain.CloudAgentSession, error) {
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
	worker, _, key, err := handler.workerExecutionInputs(ctx, cloudSession.WorkerID)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, fmt.Errorf("%w: %s", storage.ErrNotFound, handler.requestText(r, "Worker SSH 配置不存在，无法连接 shell。", "The worker SSH configuration is missing, so the shell cannot be opened."))
	}
	if err != nil {
		return domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, err
	}
	account.WorkspacePath = firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath))
	return worker, key, account, cloudSession, nil
}

func (handler *Handler) resolveConsoleChatShellTarget(ctx context.Context, r *http.Request) (domain.WorkerServer, domain.WorkerSSHKey, domain.CloudAgentAccount, domain.CloudAgentSession, error) {
	return handler.resolveConsoleChatCloudAgentTarget(ctx, r, r.URL.Query().Get("session"))
}

func (handler *Handler) chatShellPageStateForSession(ctx context.Context, r *http.Request, chatSessionID string) (consoleChatShellPageState, error) {
	worker, _, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(ctx, r, chatSessionID)
	if err != nil {
		return consoleChatShellPageState{}, err
	}
	chatSessionID = firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(chatSessionID))
	return consoleChatShellPageState{
		SessionID:     chatSessionID,
		WorkerID:      worker.ID,
		ContainerName: account.ContainerName,
		WorkspacePath: firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath), domain.DefaultCloudAgentWorkspacePath),
		SocketURL:     consoleChatShellSocketPath + "?session=" + url.QueryEscape(chatSessionID),
		ChatURL:       consoleChatEndpoint + "?session=" + url.QueryEscape(chatSessionID),
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
			Status:    "error",
			SessionID: sessionID,
			Error:     err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatShellReadyResponse{
		Status:        "ready",
		SessionID:     state.SessionID,
		Environment:   consoleChatEnvironmentValue(state.WorkerID),
		WorkerID:      state.WorkerID,
		ContainerName: state.ContainerName,
		WorkspacePath: state.WorkspacePath,
		SocketURL:     state.SocketURL,
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
	shell, err := handler.openCloudAgentShell(context.Background(), worker, key, account, cloudSession, consoleChatShellCols, consoleChatShellRows)
	if err != nil {
		_ = websocket.JSON.Send(ws, consoleChatShellSocketEvent{Type: "error", Message: err.Error()})
		return
	}
	defer shell.Close()

	var sendMu sync.Mutex
	send := func(event consoleChatShellSocketEvent) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return websocket.JSON.Send(ws, event)
	}
	_ = send(consoleChatShellSocketEvent{Type: "ready", Message: handler.requestText(r, "Claude Code 已连接", "Claude Code connected")})

	shellDone := make(chan error, 1)
	go func() {
		shellDone <- streamConsoleChatShellOutput(shell, send)
	}()

	clientDone := make(chan error, 1)
	go func() {
		clientDone <- receiveConsoleChatShellInput(ws, shell)
	}()

	var finalErr error
	select {
	case finalErr = <-shellDone:
	case finalErr = <-clientDone:
	}
	_ = shell.Close()
	closedMessage := handler.requestText(r, "Claude Code 已断开", "Claude Code disconnected")
	if finalErr != nil && !consoleChatShellIgnorableError(finalErr) {
		_ = send(consoleChatShellSocketEvent{Type: "error", Message: finalErr.Error()})
		closedMessage = finalErr.Error()
	}
	_ = send(consoleChatShellSocketEvent{Type: "closed", Message: closedMessage})
}

func streamConsoleChatShellOutput(shell workerops.InteractiveShell, send func(consoleChatShellSocketEvent) error) error {
	buffer := make([]byte, 4096)
	for {
		count, err := shell.Read(buffer)
		if count > 0 {
			if sendErr := send(consoleChatShellSocketEvent{Type: "output", Data: string(buffer[:count])}); sendErr != nil {
				return sendErr
			}
		}
		if err != nil {
			return err
		}
	}
}

func receiveConsoleChatShellInput(ws *websocket.Conn, shell workerops.InteractiveShell) error {
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
