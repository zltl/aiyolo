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
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/zltl/aiyolo/internal/domain"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatBrowserCDPBasePath      = "/console/chat/browser/cdp"
	consoleChatBrowserCDPRoutePrefix   = "/chat/browser/cdp"
	consoleChatBrowserCDPWebSocketPath = "/console/chat/browser/cdp/ws"
	consoleChatBrowserScreenshotPath   = "/console/chat/browser/screenshot"
	consoleChatBrowserMCPPath          = "/console/chat/browser/mcp"
)

type consoleChatBrowserScreenshotRequest struct {
	SessionID string `json:"sessionId"`
}

type consoleChatBrowserScreenshotResponse struct {
	Status     string                     `json:"status"`
	Attachment *consoleChatAttachmentView `json:"attachment,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

func (handler *Handler) chatBrowserCDPProxy(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	worker, key, _, _, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	suffix := strings.TrimPrefix(r.URL.Path, consoleChatBrowserCDPRoutePrefix)
	if suffix == "" {
		suffix = "/json"
	}
	if r.URL.RawQuery != "" {
		if strings.Contains(suffix, "?") {
			suffix += "&" + r.URL.RawQuery
		} else {
			suffix += "?" + r.URL.RawQuery
		}
	}
	payload, err := workerops.CloudAgentChromeHTTPGet(r.Context(), worker, key, userID, suffix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(payload)
}

func (handler *Handler) chatBrowserCDPWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	wsPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if sessionID == "" || wsPath == "" {
		http.Error(w, handler.requestText(r, "缺少 session 或 CDP path。", "Missing session or CDP path."), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(wsPath, "/devtools/") {
		http.Error(w, handler.requestText(r, "CDP path 无效。", "Invalid CDP path."), http.StatusBadRequest)
		return
	}
	worker, key, _, _, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	server := websocket.Server{
		Handler: func(clientWS *websocket.Conn) {
			handler.serveChatBrowserCDPSocket(clientWS, r.Context(), worker, key, userID, wsPath)
		},
	}
	server.ServeHTTP(w, r)
}

func (handler *Handler) serveChatBrowserCDPSocket(clientWS *websocket.Conn, ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, wsPath string) {
	_ = handler
	chromeWS, err := dialConsoleChromeDevToolsWS(ctx, worker, key, userID, wsPath)
	if err != nil {
		log.Printf("chat browser cdp dial failed: %v", err)
		return
	}
	defer chromeWS.Close()

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			_ = clientWS.Close()
			_ = chromeWS.Close()
		})
	}

	go func() {
		defer closeAll()
		for {
			var payload []byte
			if err := websocket.Message.Receive(chromeWS, &payload); err != nil {
				if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
					log.Printf("chat browser cdp chrome read failed: %v", err)
				}
				return
			}
			if len(payload) == 0 {
				continue
			}
			if err := websocket.Message.Send(clientWS, payload); err != nil {
				return
			}
		}
	}()

	for {
		var payload []byte
		if err := websocket.Message.Receive(clientWS, &payload); err != nil {
			return
		}
		if len(payload) == 0 {
			continue
		}
		if err := websocket.Message.Send(chromeWS, payload); err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				log.Printf("chat browser cdp chrome write failed: %v", err)
			}
			return
		}
	}
}

func (handler *Handler) chatBrowserScreenshot(w http.ResponseWriter, r *http.Request) {
	var payload consoleChatBrowserScreenshotRequest
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserScreenshotResponse{
			Status: "error",
			Error:  handler.requestText(r, "截图请求无效。", "The screenshot request is invalid."),
		})
		return
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserScreenshotResponse{
			Status: "error",
			Error:  handler.requestText(r, "缺少 session。", "Missing session."),
		})
		return
	}
	worker, key, _, _, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserScreenshotResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	attachment, err := handler.captureBrowserScreenshotAttachment(ctx, worker, key, userID)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(consoleChatBrowserScreenshotResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatBrowserScreenshotResponse{
		Status:     "ok",
		Attachment: &attachment,
	})
}

func (handler *Handler) captureBrowserScreenshotAttachment(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) (consoleChatAttachmentView, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	png, err := workerops.CaptureCloudAgentChromeScreenshot(ctx, worker, key, userID)
	if err != nil {
		return consoleChatAttachmentView{}, err
	}
	return handler.publishBrowserScreenshot(ctx, userID, png)
}

func (handler *Handler) publishBrowserScreenshot(ctx context.Context, userID string, png []byte) (consoleChatAttachmentView, error) {
	if len(png) == 0 {
		return consoleChatAttachmentView{}, fmt.Errorf("screenshot payload is empty")
	}
	objectKey := path.Join(
		"chat",
		sanitizeConsoleChatAttachmentPart(userID),
		"browser",
		time.Now().UTC().Format("2006/01/02"),
		newConsoleID("shot")+".png",
	)
	publisher, err := handler.newChatAttachmentPublisher(handler.cfg.ChatAttachments)
	if err != nil {
		return consoleChatAttachmentView{}, err
	}
	published, err := publisher.UploadBytes(ctx, png, objectKey, "image/png")
	if err != nil {
		return consoleChatAttachmentView{}, err
	}
	attachment := consoleChatAttachmentView{
		ID:        newConsoleID("att"),
		Name:      path.Base(objectKey),
		ObjectKey: published.ObjectKey,
		MediaType: "image/png",
		SizeBytes: int64(len(png)),
	}
	normalized, ok := normalizeConsoleChatAttachment(handler.cfg.ChatAttachments, attachment)
	if !ok {
		return consoleChatAttachmentView{}, fmt.Errorf("normalize browser screenshot attachment failed")
	}
	return normalized, nil
}

func (handler *Handler) captureBrowserScreenshotURL(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) (string, error) {
	attachment, err := handler.captureBrowserScreenshotAttachment(ctx, worker, key, userID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(attachment.URL), nil
}

func dialConsoleChromeDevToolsWS(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, wsPath string) (*websocket.Conn, error) {
	conn, err := workerops.DialCloudAgentChrome(ctx, worker, key, userID)
	if err != nil {
		return nil, err
	}
	wsConfig, err := websocket.NewConfig("ws://127.0.0.1"+wsPath, "http://127.0.0.1/")
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	ws, err := websocket.NewClient(wsConfig, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ws, nil
}
