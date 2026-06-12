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
	"strings"
	"sync"

	"golang.org/x/net/websocket"

	"github.com/zltl/aiyolo/internal/domain"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatBrowserViewPath     = "/console/chat/browser/view"
	consoleChatBrowserSocketPath   = "/console/chat/browser/ws"
	consoleChatBrowserReadyPath    = "/console/chat/browser/ready"
	consoleChatBrowserNavigatePath = "/console/chat/browser/navigate"
)

type consoleChatBrowserReadyResponse struct {
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	ViewURL       string `json:"viewUrl,omitempty"`
	SocketURL     string `json:"socketUrl,omitempty"`
	CDPURL        string `json:"cdpUrl,omitempty"`
	ScreenshotURL string `json:"screenshotUrl,omitempty"`
	MCPURL        string `json:"mcpUrl,omitempty"`
	Error         string `json:"error,omitempty"`
}

type consoleChatBrowserNavigateRequest struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
}

type consoleChatBrowserNavigateResponse struct {
	Status  string `json:"status"`
	Notice  string `json:"notice,omitempty"`
	Error   string `json:"error,omitempty"`
}

func consoleChatBrowserSocketURL(chatSessionID string) string {
	query := url.Values{}
	query.Set("session", strings.TrimSpace(chatSessionID))
	return consoleChatBrowserSocketPath + "?" + query.Encode()
}

func consoleChatBrowserViewURL(chatSessionID string) string {
	query := url.Values{}
	query.Set("session", strings.TrimSpace(chatSessionID))
	return consoleChatBrowserViewPath + "?" + query.Encode()
}

func (handler *Handler) chatBrowserReady(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	worker, _, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserReadyResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatBrowserReadyResponse{
		Status:        "ready",
		SessionID:     sessionID,
		Environment:   consoleChatEnvironmentValue(worker.ID),
		WorkerID:      worker.ID,
		ContainerName: account.ContainerName,
		ViewURL:       consoleChatBrowserViewURL(firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), sessionID)),
		SocketURL:     consoleChatBrowserSocketURL(firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), sessionID)),
		CDPURL:        consoleChatBrowserCDPURL(firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), sessionID)),
		ScreenshotURL: consoleChatBrowserScreenshotPath,
		MCPURL:        consoleChatBrowserMCPURL(firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), sessionID)),
	})
}

func consoleChatBrowserCDPURL(chatSessionID string) string {
	query := url.Values{}
	query.Set("session", strings.TrimSpace(chatSessionID))
	return consoleChatBrowserCDPBasePath + "/json?" + query.Encode()
}

func consoleChatBrowserMCPURL(chatSessionID string) string {
	query := url.Values{}
	query.Set("session", strings.TrimSpace(chatSessionID))
	return consoleChatBrowserMCPPath + "?" + query.Encode()
}

func (handler *Handler) chatBrowserView(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionID == "" {
		http.Error(w, handler.requestText(r, "缺少 chat session。", "Missing chat session."), http.StatusBadRequest)
		return
	}
	if _, _, _, _, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	socketURL := consoleChatBrowserSocketURL(sessionID)
	if r.TLS != nil {
		socketURL = "wss://" + r.Host + socketURL
	} else {
		socketURL = "ws://" + r.Host + socketURL
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Cloud Agent Browser</title>
  <style>
    html, body { margin: 0; height: 100%%; background: #111; overflow: hidden; }
    #screen { width: 100%%; height: 100%%; }
    #status { position: fixed; left: 12px; right: 12px; bottom: 12px; padding: 8px 10px; border-radius: 6px; background: rgba(20,20,20,.88); color: #ccc; font: 13px/1.4 sans-serif; }
  </style>
</head>
<body>
  <div id="screen"></div>
  <div id="status">Connecting…</div>
  <script type="module">
    import RFB from "https://esm.sh/@novnc/novnc@1.5.0/core/rfb.js";
    const status = document.getElementById("status");
    const screen = document.getElementById("screen");
    const socketURL = %q;
    const rfb = new RFB(screen, socketURL, { shared: true, showDotCursor: true });
    rfb.scaleViewport = true;
    rfb.resizeSession = true;
    rfb.background = "#111111";
    rfb.addEventListener("connect", () => { status.textContent = "Connected"; status.hidden = true; });
    rfb.addEventListener("disconnect", (event) => {
      status.hidden = false;
      status.textContent = event.detail.clean ? "Disconnected" : "Browser connection lost";
    });
    rfb.addEventListener("securityfailure", (event) => {
      status.hidden = false;
      status.textContent = "VNC security failure: " + (event.detail.status || "unknown");
    });
  </script>
</body>
</html>`, socketURL)
}

func consoleChatBrowserShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func (handler *Handler) chatBrowserNavigate(w http.ResponseWriter, r *http.Request) {
	var payload consoleChatBrowserNavigateRequest
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserNavigateResponse{
			Status: "error",
			Error:  handler.requestText(r, "浏览器导航请求无效。", "The browser navigation request is invalid."),
		})
		return
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	targetURL := strings.TrimSpace(payload.URL)
	if sessionID == "" || targetURL == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserNavigateResponse{
			Status: "error",
			Error:  handler.requestText(r, "缺少 session 或 URL。", "Missing session or URL."),
		})
		return
	}
	worker, key, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserNavigateResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	script := fmt.Sprintf(
		"docker exec -u aiyolo %s /usr/local/bin/aiyolo-cloud-agent-open-chrome %s",
		consoleChatBrowserShellQuote(strings.TrimSpace(account.ContainerName)),
		consoleChatBrowserShellQuote(targetURL),
	)
	if _, err := workerops.RunCloudAgentCommand(r.Context(), worker, key, account, cloudSession, script); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserNavigateResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatBrowserNavigateResponse{
		Status: "ok",
		Notice: handler.requestText(r, "已在容器浏览器中打开页面。", "Opened the page in the container browser."),
	})
}

func (handler *Handler) chatBrowserSocket(w http.ResponseWriter, r *http.Request) {
	worker, key, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	server := websocket.Server{
		Handler: func(ws *websocket.Conn) {
			handler.serveChatBrowserSocket(ws, r, worker, key, account, cloudSession, userID)
		},
	}
	server.ServeHTTP(w, r)
}

func (handler *Handler) serveChatBrowserSocket(ws *websocket.Conn, r *http.Request, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, userID string) {
	_ = account
	_ = cloudSession
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	vncConn, err := workerops.DialCloudAgentVNC(ctx, worker, key, userID)
	if err != nil {
		log.Printf("chat browser vnc dial failed: %v", err)
		return
	}
	defer vncConn.Close()

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			_ = ws.Close()
			_ = vncConn.Close()
		})
	}

	go func() {
		defer closeAll()
		buffer := make([]byte, 32*1024)
		for {
			count, readErr := vncConn.Read(buffer)
			if count > 0 {
				if sendErr := websocket.Message.Send(ws, buffer[:count]); sendErr != nil {
					return
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, net.ErrClosed) && !errors.Is(readErr, io.EOF) {
					log.Printf("chat browser vnc read failed: %v", readErr)
				}
				return
			}
		}
	}()

	for {
		var payload []byte
		if err := websocket.Message.Receive(ws, &payload); err != nil {
			return
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := vncConn.Write(payload); err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				log.Printf("chat browser vnc write failed: %v", err)
			}
			return
		}
	}
}
