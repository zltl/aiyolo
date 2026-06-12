package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

type consoleChatMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type consoleChatMCPResponse struct {
	JSONRPC string               `json:"jsonrpc"`
	ID      json.RawMessage      `json:"id,omitempty"`
	Result  any                  `json:"result,omitempty"`
	Error   *consoleChatMCPError `json:"error,omitempty"`
}

type consoleChatMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type consoleChatMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (handler *Handler) chatBrowserMCP(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionID == "" {
		http.Error(w, handler.requestText(r, "缺少 chat session。", "Missing chat session."), http.StatusBadRequest)
		return
	}
	userID, err := handler.resolveBrowserMCPAccess(r, sessionID)
	if err != nil {
		http.Error(w, handler.requestText(r, "未授权访问 browser MCP。", "Unauthorized browser MCP access."), http.StatusUnauthorized)
		return
	}
	worker, key, _, _, err := handler.resolveConsoleChatCloudAgentTargetForUser(r.Context(), r, userID, sessionID, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handler.writeChatBrowserMCPCapabilities(w, sessionID)
		return
	case http.MethodPost:
		var payload consoleChatMCPRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, handler.requestText(r, "MCP 请求无效。", "Invalid MCP request."), http.StatusBadRequest)
			return
		}
		response := handler.handleChatBrowserMCPRequest(r.Context(), r, worker, key, userID, payload)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(response)
		return
	default:
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func (handler *Handler) writeChatBrowserMCPCapabilities(w http.ResponseWriter, sessionID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    "aiyolo-cloud-agent-browser",
			"version": "0.1.0",
		},
		"transport": "http",
		"endpoint":  consoleChatBrowserMCPPath + "?session=" + sessionID,
		"tools":     consoleChatBrowserMCPTools(),
	})
}

func consoleChatBrowserMCPTools() []consoleChatMCPTool {
	return []consoleChatMCPTool{
		{
			Name:        "browser_navigate",
			Description: "Navigate the cloud agent Chrome browser to a URL.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string"},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "browser_screenshot",
			Description: "Capture a PNG screenshot of the current cloud agent browser page.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "browser_snapshot",
			Description: "Capture a compact DOM snapshot of the current cloud agent browser page.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func (handler *Handler) handleChatBrowserMCPRequest(ctx context.Context, r *http.Request, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, payload consoleChatMCPRequest) consoleChatMCPResponse {
	response := consoleChatMCPResponse{JSONRPC: "2.0", ID: payload.ID}
	method := strings.TrimSpace(payload.Method)
	switch method {
	case "initialize":
		response.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "aiyolo-cloud-agent-browser",
				"version": "0.1.0",
			},
		}
	case "tools/list":
		response.Result = map[string]any{"tools": consoleChatBrowserMCPTools()}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(payload.Params, &params); err != nil {
			response.Error = &consoleChatMCPError{Code: -32602, Message: "invalid tools/call params"}
			return response
		}
		result, err := handler.callChatBrowserMCPTool(ctx, r, worker, key, userID, params.Name, params.Arguments)
		if err != nil {
			response.Error = &consoleChatMCPError{Code: -32000, Message: err.Error()}
			return response
		}
		response.Result = result
	default:
		response.Error = &consoleChatMCPError{Code: -32601, Message: "method not found: " + method}
	}
	return response
}

func (handler *Handler) callChatBrowserMCPTool(ctx context.Context, r *http.Request, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, name string, arguments map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	switch strings.TrimSpace(name) {
	case "browser_navigate":
		targetURL := strings.TrimSpace(stringValue(arguments["url"]))
		if targetURL == "" {
			return nil, fmt.Errorf("url is required")
		}
		if err := workerops.CloudAgentChromeNavigate(ctx, worker, key, userID, targetURL); err != nil {
			return nil, err
		}
		return map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": handler.requestText(r, "已在容器浏览器中打开页面。", "Opened the page in the container browser."),
			}},
		}, nil
	case "browser_screenshot":
		attachment, err := handler.captureBrowserScreenshotAttachment(ctx, worker, key, userID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": attachment.URL},
				{"type": "image", "mimeType": attachment.MediaType, "url": attachment.URL},
			},
		}, nil
	case "browser_snapshot":
		snapshot, err := workerops.CloudAgentChromeSnapshot(ctx, worker, key, userID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": snapshot,
			}},
		}, nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}
