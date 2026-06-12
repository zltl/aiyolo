package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatBrowserMCPStatusPath = "/console/chat/browser/mcp/status"
	consoleChatBrowserMCPConfigPath = "/console/chat/browser/mcp/config"
)

type consoleChatBrowserMCPStatusResponse struct {
	Status      string `json:"status"`
	Enabled     bool   `json:"enabled"`
	Configured  bool   `json:"configured"`
	Connected   bool   `json:"connected"`
	ToolCount   int    `json:"toolCount,omitempty"`
	MCPURL      string `json:"mcpUrl,omitempty"`
	Notice      string `json:"notice,omitempty"`
	Error       string `json:"error,omitempty"`
}

type consoleChatBrowserMCPConfigRequest struct {
	SessionID string `json:"sessionId"`
	Enabled   bool   `json:"enabled"`
}

type consoleChatBrowserMCPConfigResponse struct {
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
	Notice  string `json:"notice,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (handler *Handler) chatBrowserMCPStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if sessionID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserMCPStatusResponse{
			Status: "error",
			Error:  handler.requestText(r, "缺少 chat session。", "Missing chat session."),
		})
		return
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	response := handler.buildChatBrowserMCPStatus(r.Context(), r, userID, sessionID)
	if response.Status == "error" && response.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (handler *Handler) chatBrowserMCPConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var payload consoleChatBrowserMCPConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserMCPConfigResponse{
			Status: "error",
			Error:  handler.requestText(r, "MCP 配置请求无效。", "Invalid MCP config request."),
		})
		return
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserMCPConfigResponse{
			Status: "error",
			Error:  handler.requestText(r, "缺少 chat session。", "Missing chat session."),
		})
		return
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	if err := handler.setConsoleChatBrowserMCPEnabled(r.Context(), userID, sessionID, payload.Enabled); err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusBadRequest
		}
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserMCPConfigResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	worker, key, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTargetForUser(r.Context(), r, userID, sessionID, false)
	if err == nil {
		baseURL := consoleChatCloudAgentBaseURL(handler.codexPublicBaseURL(r), worker)
		if payload.Enabled {
			_ = handler.syncConsoleChatBrowserMCPConfig(r.Context(), worker, key, account, cloudSession, baseURL, userID, sessionID)
		} else {
			_ = handler.clearConsoleChatBrowserMCPConfig(r.Context(), worker, key, account, cloudSession)
		}
	}
	var notice string
	if payload.Enabled {
		notice = handler.requestText(r, "Browser MCP 已开启，Claude Code 可使用容器浏览器工具。", "Browser MCP is enabled. Claude Code can use the container browser tools.")
	} else {
		notice = handler.requestText(r, "Browser MCP 已关闭。", "Browser MCP is disabled.")
	}
	_ = json.NewEncoder(w).Encode(consoleChatBrowserMCPConfigResponse{
		Status:  "ok",
		Enabled: payload.Enabled,
		Notice:  notice,
	})
}

func (handler *Handler) buildChatBrowserMCPStatus(ctx context.Context, r *http.Request, userID, sessionID string) consoleChatBrowserMCPStatusResponse {
	enabled, err := handler.consoleChatBrowserMCPEnabled(ctx, userID, sessionID)
	if err != nil {
		return consoleChatBrowserMCPStatusResponse{
			Status: "error",
			Error:  err.Error(),
		}
	}
	response := consoleChatBrowserMCPStatusResponse{
		Status:     "disabled",
		Enabled:    enabled,
		MCPURL:     consoleChatBrowserMCPURL(sessionID),
		ToolCount:  len(consoleChatBrowserMCPTools()),
	}
	if !enabled {
		response.Notice = handler.requestText(r, "Browser MCP 已关闭。", "Browser MCP is disabled.")
		return response
	}
	worker, key, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTargetForUser(ctx, r, userID, sessionID, false)
	if err != nil {
		response.Status = "unavailable"
		response.Notice = handler.requestText(r, "Cloud Agent 未就绪，Browser MCP 暂不可用。", "Cloud Agent is not ready, so Browser MCP is unavailable.")
		return response
	}
	configured, configErr := handler.consoleChatBrowserMCPConfiguredInContainer(ctx, worker, key, account, cloudSession)
	if configErr != nil {
		response.Status = "error"
		response.Error = configErr.Error()
		return response
	}
	response.Configured = configured
	response.Connected = configured && strings.TrimSpace(cloudSession.Status) == domain.CloudAgentSessionStatusActive
	if response.Connected {
		response.Status = "ready"
		response.Notice = handler.requestText(r, "Browser MCP 已连接。", "Browser MCP is connected.")
		return response
	}
	if configured {
		response.Status = "pending"
		response.Notice = handler.requestText(r, "Browser MCP 已配置，等待 Claude Code 连接。", "Browser MCP is configured and waiting for Claude Code.")
		return response
	}
	response.Status = "pending"
	response.Notice = handler.requestText(r, "Browser MCP 正在同步到容器…", "Browser MCP is syncing to the container...")
	return response
}

func (handler *Handler) consoleChatBrowserMCPConfiguredInContainer(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession) (bool, error) {
	output, err := handler.runCloudAgentCommand(ctx, worker, key, account, cloudSession, workerops.BuildCloudAgentBrowserMCPConfiguredShell())
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "yes", nil
}
