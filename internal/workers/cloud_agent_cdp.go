package workers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"

	"github.com/zltl/aiyolo/internal/domain"
)

// CloudAgentChromeHostPort returns the loopback host port where the user's cloud-agent
// container exposes Chrome DevTools on the worker machine.
func CloudAgentChromeHostPort(userID, workerID string) int {
	return cloudAgentHostPort(userID, workerID+"-chrome", defaultCloudAgentHostChromeBasePort)
}

// CloudAgentChromeTarget describes a Chrome DevTools debug target.
type CloudAgentChromeTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// DialCloudAgentChrome opens a TCP connection to the cloud-agent Chrome DevTools endpoint.
func DialCloudAgentChrome(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) (net.Conn, error) {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(CloudAgentChromeHostPort(userID, worker.ID)))
	if WorkerIsLocal(worker) {
		dialer := net.Dialer{Timeout: 10 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, fmt.Errorf("dial local cloud agent chrome %s: %w", address, err)
		}
		return conn, nil
	}
	client, err := dialSSHStreaming(worker, key)
	if err != nil {
		return nil, err
	}
	conn, err := client.Dial("tcp", address)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("dial remote cloud agent chrome %s: %w", address, err)
	}
	return &sshForwardedConn{Client: client, Conn: conn}, nil
}

// ListCloudAgentChromeTargets returns Chrome DevTools targets from /json/list.
func ListCloudAgentChromeTargets(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) ([]CloudAgentChromeTarget, error) {
	payload, err := cloudAgentChromeHTTPGet(ctx, worker, key, userID, "/json/list")
	if err != nil {
		return nil, err
	}
	var targets []CloudAgentChromeTarget
	if err := json.Unmarshal(payload, &targets); err != nil {
		return nil, fmt.Errorf("decode chrome devtools targets: %w", err)
	}
	return targets, nil
}

// CaptureCloudAgentChromeScreenshot captures the active Chrome page via CDP Page.captureScreenshot.
func CaptureCloudAgentChromeScreenshot(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) ([]byte, error) {
	targets, err := ListCloudAgentChromeTargets(ctx, worker, key, userID)
	if err != nil {
		return nil, err
	}
	target, ok := pickCloudAgentChromePageTarget(targets)
	if !ok {
		return nil, fmt.Errorf("no chrome page target available")
	}
	wsPath := cloudAgentChromeDevToolsWSPath(target.WebSocketDebuggerURL)
	if wsPath == "" {
		return nil, fmt.Errorf("chrome target missing websocket debugger path")
	}
	return captureCloudAgentChromeScreenshotCDP(ctx, worker, key, userID, wsPath)
}

func pickCloudAgentChromePageTarget(targets []CloudAgentChromeTarget) (CloudAgentChromeTarget, bool) {
	for _, target := range targets {
		if strings.EqualFold(strings.TrimSpace(target.Type), "page") && strings.TrimSpace(target.WebSocketDebuggerURL) != "" {
			return target, true
		}
	}
	for _, target := range targets {
		if strings.TrimSpace(target.WebSocketDebuggerURL) != "" {
			return target, true
		}
	}
	return CloudAgentChromeTarget{}, false
}

func cloudAgentChromeDevToolsWSPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Path == "" {
		return ""
	}
	if parsed.RawQuery != "" {
		return parsed.Path + "?" + parsed.RawQuery
	}
	return parsed.Path
}

func cloudAgentChromeHTTPGet(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, endpointPath string) ([]byte, error) {
	return cloudAgentChromeHTTPRequest(ctx, worker, key, userID, http.MethodGet, endpointPath, nil)
}

// CloudAgentChromeHTTPGet proxies an HTTP GET to Chrome DevTools on the worker loopback port.
func CloudAgentChromeHTTPGet(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, endpointPath string) ([]byte, error) {
	return cloudAgentChromeHTTPRequest(ctx, worker, key, userID, http.MethodGet, endpointPath, nil)
}

func cloudAgentChromeHTTPRequest(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, method string, endpointPath string, body io.Reader) ([]byte, error) {
	transport, cleanup, err := newCloudAgentChromeTransport(ctx, worker, key, userID)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	request, err := http.NewRequestWithContext(ctx, method, "http://chrome.local"+endpointPath, body)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("chrome devtools GET %s: %w", endpointPath, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		detail := strings.TrimSpace(string(payload))
		if detail == "" {
			detail = response.Status
		}
		return nil, fmt.Errorf("chrome devtools GET %s returned HTTP %d: %s", endpointPath, response.StatusCode, detail)
	}
	return payload, nil
}

func newCloudAgentChromeTransport(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) (*http.Transport, func(), error) {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(CloudAgentChromeHostPort(userID, worker.ID)))
	if WorkerIsLocal(worker) {
		transport := &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "tcp", address)
			},
		}
		return transport, func() { transport.CloseIdleConnections() }, nil
	}
	sshClient, err := dialSSHStreaming(worker, key)
	if err != nil {
		return nil, nil, err
	}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return sshClient.Dial("tcp", address)
		},
	}
	return transport, func() {
		transport.CloseIdleConnections()
		_ = sshClient.Close()
	}, nil
}

type cloudAgentCDPMessage struct {
	ID     int            `json:"id"`
	Method string         `json:"method,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Result map[string]any `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func captureCloudAgentChromeScreenshotCDP(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, wsPath string) ([]byte, error) {
	ws, err := dialCloudAgentChromeDevToolsWS(ctx, worker, key, userID, wsPath)
	if err != nil {
		return nil, err
	}
	defer ws.Close()

	var nextID int32
	send := func(method string, params map[string]any) (cloudAgentCDPMessage, error) {
		id := int(atomic.AddInt32(&nextID, 1))
		payload, err := json.Marshal(cloudAgentCDPMessage{ID: id, Method: method, Params: params})
		if err != nil {
			return cloudAgentCDPMessage{}, err
		}
		if err := websocket.Message.Send(ws, payload); err != nil {
			return cloudAgentCDPMessage{}, err
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			var raw []byte
			if err := websocket.Message.Receive(ws, &raw); err != nil {
				return cloudAgentCDPMessage{}, err
			}
			var message cloudAgentCDPMessage
			if err := json.Unmarshal(raw, &message); err != nil {
				continue
			}
			if message.ID != id {
				continue
			}
			if message.Error != nil {
				return cloudAgentCDPMessage{}, fmt.Errorf("cdp %s: %s", method, strings.TrimSpace(message.Error.Message))
			}
			return message, nil
		}
		return cloudAgentCDPMessage{}, fmt.Errorf("cdp %s timed out", method)
	}

	if _, err := send("Page.enable", nil); err != nil {
		return nil, fmt.Errorf("enable page: %w", err)
	}
	response, err := send("Page.captureScreenshot", map[string]any{
		"format":                "png",
		"captureBeyondViewport": true,
		"fromSurface":           true,
	})
	if err != nil {
		return nil, err
	}
	encoded := strings.TrimSpace(cloudAgentCDPStringValue(response.Result["data"]))
	if encoded == "" {
		return nil, fmt.Errorf("chrome screenshot returned empty payload")
	}
	png, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode chrome screenshot: %w", err)
	}
	return png, nil
}

// CloudAgentChromeNavigate uses CDP Page.navigate on the first page target.
func CloudAgentChromeNavigate(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, targetURL string) error {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return fmt.Errorf("target url is empty")
	}
	targets, err := ListCloudAgentChromeTargets(ctx, worker, key, userID)
	if err != nil {
		return err
	}
	target, ok := pickCloudAgentChromePageTarget(targets)
	if !ok {
		return fmt.Errorf("no chrome page target available")
	}
	wsPath := cloudAgentChromeDevToolsWSPath(target.WebSocketDebuggerURL)
	if wsPath == "" {
		return fmt.Errorf("chrome target missing websocket debugger path")
	}
	return cloudAgentChromeNavigateCDP(ctx, worker, key, userID, wsPath, targetURL)
}

func cloudAgentChromeNavigateCDP(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, wsPath string, targetURL string) error {
	ws, err := dialCloudAgentChromeDevToolsWS(ctx, worker, key, userID, wsPath)
	if err != nil {
		return err
	}
	defer ws.Close()

	payload, err := json.Marshal(cloudAgentCDPMessage{
		ID:     1,
		Method: "Page.navigate",
		Params: map[string]any{"url": targetURL},
	})
	if err != nil {
		return err
	}
	if err := websocket.Message.Send(ws, payload); err != nil {
		return err
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var raw []byte
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return err
		}
		var message cloudAgentCDPMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			continue
		}
		if message.ID != 1 {
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("cdp Page.navigate: %s", strings.TrimSpace(message.Error.Message))
		}
		return nil
	}
	return fmt.Errorf("cdp Page.navigate timed out")
}

// CloudAgentChromeSnapshot returns a compact DOM snapshot via CDP.
func CloudAgentChromeSnapshot(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) (string, error) {
	targets, err := ListCloudAgentChromeTargets(ctx, worker, key, userID)
	if err != nil {
		return "", err
	}
	target, ok := pickCloudAgentChromePageTarget(targets)
	if !ok {
		return "", fmt.Errorf("no chrome page target available")
	}
	wsPath := cloudAgentChromeDevToolsWSPath(target.WebSocketDebuggerURL)
	if wsPath == "" {
		return "", fmt.Errorf("chrome target missing websocket debugger path")
	}
	return cloudAgentChromeSnapshotCDP(ctx, worker, key, userID, wsPath)
}

func cloudAgentChromeSnapshotCDP(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, wsPath string) (string, error) {
	ws, err := dialCloudAgentChromeDevToolsWS(ctx, worker, key, userID, wsPath)
	if err != nil {
		return "", err
	}
	defer ws.Close()

	send := func(id int, method string, params map[string]any) (cloudAgentCDPMessage, error) {
		payload, err := json.Marshal(cloudAgentCDPMessage{ID: id, Method: method, Params: params})
		if err != nil {
			return cloudAgentCDPMessage{}, err
		}
		if err := websocket.Message.Send(ws, payload); err != nil {
			return cloudAgentCDPMessage{}, err
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			var raw []byte
			if err := websocket.Message.Receive(ws, &raw); err != nil {
				return cloudAgentCDPMessage{}, err
			}
			var message cloudAgentCDPMessage
			if err := json.Unmarshal(raw, &message); err != nil {
				continue
			}
			if message.ID != id {
				continue
			}
			if message.Error != nil {
				return cloudAgentCDPMessage{}, fmt.Errorf("cdp %s: %s", method, strings.TrimSpace(message.Error.Message))
			}
			return message, nil
		}
		return cloudAgentCDPMessage{}, fmt.Errorf("cdp %s timed out", method)
	}

	if _, err := send(1, "DOM.enable", nil); err != nil {
		return "", err
	}
	response, err := send(2, "DOM.getDocument", map[string]any{"depth": 2, "pierce": false})
	if err != nil {
		return "", err
	}
	root, _ := response.Result["root"].(map[string]any)
	if len(root) == 0 {
		return "", fmt.Errorf("chrome snapshot returned empty document")
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func cloudAgentCDPStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func dialCloudAgentChromeDevToolsWS(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string, wsPath string) (*websocket.Conn, error) {
	conn, err := DialCloudAgentChrome(ctx, worker, key, userID)
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
		return nil, fmt.Errorf("dial chrome devtools websocket: %w", err)
	}
	return ws, nil
}
