package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/websocket"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

func TestChatEnvironmentEnsureEndpointStartsCloudAgent(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	var ensureCalls atomic.Int32
	var ensured workerops.CloudAgentStartOptions
	handler := NewHandler(Config{
		SecretKey:          "test-secret",
		AdminEmail:         "admin@example.com",
		AdminPassword:      "password",
		CodexPublicBaseURL: "https://aiyolo.quant67.com",
	}, store)
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		ensured = options
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || proxy.ID != domain.ProxyTypeDirect {
			t.Fatalf("unexpected ensure inputs worker=%+v key=%+v proxy=%+v", worker, key, proxy)
		}
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-123",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/chat-session",
		}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.PostForm(server.URL+"/console/chat/environment/ensure", url.Values{
		"chat_public_name": {"gpt-5.4"},
		"chat_environment": {consoleChatEnvironmentValue("worker-0")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("ensure status=%d body=%s", response.StatusCode, body)
	}

	var payload consoleChatEnvironmentEnsureResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ready" || payload.WorkerID != "worker-0" || payload.Environment != consoleChatEnvironmentValue("worker-0") {
		t.Fatalf("unexpected ensure payload: %+v", payload)
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		t.Fatalf("expected ensure endpoint to generate a session id: %+v", payload)
	}
	if ensureCalls.Load() != 1 {
		t.Fatalf("ensure calls=%d", ensureCalls.Load())
	}
	if ensured.APIBaseURL != "https://aiyolo.quant67.com/v1" || ensured.ConsoleBaseURL != "https://aiyolo.quant67.com" {
		t.Fatalf("unexpected ensured URLs: %+v", ensured)
	}
	if !strings.Contains(ensured.OpenURL, "/console/chat?session="+payload.SessionID) {
		t.Fatalf("unexpected ensured open url: %s", ensured.OpenURL)
	}
	if ensured.DefaultModel != "gpt-5.4" || len(ensured.AllowedModels) != 1 || ensured.AllowedModels[0] != "gpt-5.4" {
		t.Fatalf("unexpected ensured model options: %+v", ensured)
	}

	account, err := store.GetCloudAgentAccount(ctx, "admin@example.com", consoleChatCloudAgentAccountID("worker-0"))
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != domain.CloudAgentStatusRunning || account.ContainerName != "aiyolo-cloud-agent-worker-0" || strings.TrimSpace(account.Credential) == "" {
		t.Fatalf("unexpected cloud agent account: %+v", account)
	}
	session, err := store.GetCloudAgentSession(ctx, "admin@example.com", consoleChatCloudAgentSessionID(payload.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.CloudAgentSessionStatusActive || session.ChatSessionID != payload.SessionID || session.WorkerID != "worker-0" {
		t.Fatalf("unexpected cloud agent session: %+v", session)
	}
	keys, err := store.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || len(keys[0].AllowedModels) != 1 || keys[0].AllowedModels[0] != "gpt-5.4" {
		t.Fatalf("unexpected api keys: %+v", keys)
	}
}

func TestSendChatEnsuresCloudAgentEnvironment(t *testing.T) {
	var ensureCalls atomic.Int32
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ensureCalls.Load() == 0 {
			t.Fatal("provider request happened before cloud agent ensure")
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_env_send","choices":[{"index":0,"message":{"role":"assistant","content":"Cloud container is ready."},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":6,"total_tokens":17}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, providerBackend.URL)
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	handler := NewHandler(Config{
		SecretKey:          "test-secret",
		AdminEmail:         "admin@example.com",
		AdminPassword:      "password",
		CodexPublicBaseURL: "https://aiyolo.quant67.com",
	}, store)
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || proxy.ID != domain.ProxyTypeDirect {
			t.Fatalf("unexpected ensure inputs worker=%+v key=%+v proxy=%+v", worker, key, proxy)
		}
		if !strings.Contains(options.OpenURL, "/console/chat?session=session-send-env") {
			t.Fatalf("unexpected ensured open url: %s", options.OpenURL)
		}
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-send",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/session-send-env",
		}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat", strings.NewReader(url.Values{
		"chat_client_session_id": {"session-send-env"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
		"chat_draft":             {"Can you use the cloud environment?"},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("send status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "Cloud container is ready.") {
		t.Fatalf("assistant output missing from send response: %s", body)
	}
	if ensureCalls.Load() != 1 {
		t.Fatalf("ensure calls=%d", ensureCalls.Load())
	}
	session, err := store.GetCloudAgentSession(ctx, "admin@example.com", consoleChatCloudAgentSessionID("session-send-env"))
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.CloudAgentSessionStatusActive || session.WorkerID != "worker-0" {
		t.Fatalf("unexpected cloud agent session after send: %+v", session)
	}
}

func TestStreamChatEnsuresCloudAgentEnvironment(t *testing.T) {
	var ensureCalls atomic.Int32
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ensureCalls.Load() == 0 {
			t.Fatal("provider stream happened before cloud agent ensure")
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_env_stream\",\"choices\":[{\"delta\":{\"content\":\"Cloud \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"agent is live.\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":7,\"total_tokens\":15}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, providerBackend.URL)
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	handler := NewHandler(Config{
		SecretKey:          "test-secret",
		AdminEmail:         "admin@example.com",
		AdminPassword:      "password",
		CodexPublicBaseURL: "https://aiyolo.quant67.com",
	}, store)
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		if !strings.Contains(options.OpenURL, "/console/chat?session=session-stream-env") {
			t.Fatalf("unexpected ensured open url: %s", options.OpenURL)
		}
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-stream",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/session-stream-env",
		}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", strings.NewReader(url.Values{
		"chat_client_session_id": {"session-stream-env"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
		"chat_draft":             {"Stream from the cloud agent."},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("stream status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	text := string(body)
	if !strings.Contains(text, "Cloud agent is live.") || !strings.Contains(text, `"type":"done"`) {
		t.Fatalf("unexpected stream body: %s", text)
	}
	if ensureCalls.Load() != 1 {
		t.Fatalf("ensure calls=%d", ensureCalls.Load())
	}
	session, err := store.GetCloudAgentSession(ctx, "admin@example.com", consoleChatCloudAgentSessionID("session-stream-env"))
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.CloudAgentSessionStatusActive || session.WorkerID != "worker-0" {
		t.Fatalf("unexpected cloud agent session after stream: %+v", session)
	}
}

func TestChatPageRestoresCloudAgentEnvironment(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	now := time.Now().UTC()
	accountID := consoleChatCloudAgentAccountID("worker-0")
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{
		ID:              accountID,
		UserID:          "admin@example.com",
		WorkerID:        "worker-0",
		AgentType:       domain.CloudAgentTypeClaudeCode,
		ModelPublicName: "gpt-5.4",
		ContainerName:   "aiyolo-cloud-agent-worker-0",
		WorkspacePath:   "/srv/aiyolo/workspace/session-resume",
		Credential:      "aiyolo_live_test",
		Status:          domain.CloudAgentStatusRunning,
		CreatedAt:       now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentSession(ctx, domain.CloudAgentSession{
		ID:            consoleChatCloudAgentSessionID("session-resume"),
		UserID:        "admin@example.com",
		WorkerID:      "worker-0",
		AccountID:     accountID,
		AgentType:     domain.CloudAgentTypeClaudeCode,
		ChatSessionID: "session-resume",
		WorkspacePath: "/srv/aiyolo/workspace/session-resume",
		Status:        domain.CloudAgentSessionStatusActive,
		CreatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertConsoleChatSession(ctx, domain.ConsoleChatSession{
		ID:           "session-resume",
		UserID:       "admin@example.com",
		Title:        "Resume cloud chat",
		PublicName:   "gpt-5.4",
		SystemPrompt: "Keep using the cloud workspace.",
		MessagesJSON: `[{"id":"m1","role":"user","content":"hello"}]`,
		MessageCount: 1,
		Status:       "completed",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{
		SecretKey:     "test-secret",
		AdminEmail:    "admin@example.com",
		AdminPassword: "password",
	}, store)
	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL + "/console/chat?session=session-resume")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat page status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "data-chat-environment-ensure-url=\"/console/chat/environment/ensure\"") {
		t.Fatalf("chat page is missing ensure url wiring: %s", html)
	}
	if !strings.Contains(html, "name=\"chat_environment\" value=\"cloud-agent:worker-0\" data-chat-environment-input") {
		t.Fatalf("chat page did not restore the selected environment value: %s", html)
	}
	if !strings.Contains(html, "name=\"chat_environment_choice\" value=\"cloud-agent:worker-0\" data-chat-environment-option data-chat-environment-label=\"Cloud Agent · worker-0\" checked") {
		t.Fatalf("chat page did not restore the cloud agent environment: %s", html)
	}
	if !strings.Contains(html, "data-chat-shell-url=\"/console/chat/shell\"") {
		t.Fatalf("chat page is missing shell page wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-action=\"open-shell\"") {
		t.Fatalf("chat page did not render the shell launch button: %s", html)
	}
}

func TestChatShellPageLoadsCloudAgentSession(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	now := time.Now().UTC()
	accountID := consoleChatCloudAgentAccountID("worker-0")
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{
		ID:              accountID,
		UserID:          "admin@example.com",
		WorkerID:        "worker-0",
		AgentType:       domain.CloudAgentTypeClaudeCode,
		ModelPublicName: "gpt-5.4",
		ContainerName:   "aiyolo-cloud-agent-worker-0",
		WorkspacePath:   "/srv/aiyolo/workspace/session-shell",
		Credential:      "aiyolo_live_test",
		Status:          domain.CloudAgentStatusRunning,
		CreatedAt:       now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentSession(ctx, domain.CloudAgentSession{
		ID:            consoleChatCloudAgentSessionID("session-shell"),
		UserID:        "admin@example.com",
		WorkerID:      "worker-0",
		AccountID:     accountID,
		AgentType:     domain.CloudAgentTypeClaudeCode,
		ChatSessionID: "session-shell",
		WorkspacePath: "/srv/aiyolo/workspace/session-shell",
		Status:        domain.CloudAgentSessionStatusActive,
		CreatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{
		SecretKey:     "test-secret",
		AdminEmail:    "admin@example.com",
		AdminPassword: "password",
	}, store)
	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL + "/console/chat/shell?session=session-shell")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat shell page status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "data-chat-shell-socket-url=\"/console/chat/shell/ws?session=session-shell\"") {
		t.Fatalf("chat shell page is missing socket wiring: %s", html)
	}
	if !strings.Contains(html, `https://unpkg.com/@xterm/xterm@5.5.0/css/xterm.css`) || !strings.Contains(html, `https://unpkg.com/@xterm/xterm@5.5.0/lib/xterm.js`) {
		t.Fatalf("chat shell page should load the working xterm CDN assets: %s", html)
	}
	if !strings.Contains(html, "aiyolo-cloud-agent-worker-0") || !strings.Contains(html, "/srv/aiyolo/workspace/session-shell") {
		t.Fatalf("chat shell page did not render cloud agent metadata: %s", html)
	}
	if !strings.Contains(html, `href="/console/chat?session=session-shell"`) {
		t.Fatalf("chat shell page should link back to the current chat session: %s", html)
	}
}

func TestChatShellSocketBridgesInteractiveShell(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	now := time.Now().UTC()
	accountID := consoleChatCloudAgentAccountID("worker-0")
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{
		ID:              accountID,
		UserID:          "admin@example.com",
		WorkerID:        "worker-0",
		AgentType:       domain.CloudAgentTypeClaudeCode,
		ModelPublicName: "gpt-5.4",
		ContainerName:   "aiyolo-cloud-agent-worker-0",
		WorkspacePath:   "/srv/aiyolo/workspace/session-shell",
		Credential:      "aiyolo_live_test",
		Status:          domain.CloudAgentStatusRunning,
		CreatedAt:       now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentSession(ctx, domain.CloudAgentSession{
		ID:            consoleChatCloudAgentSessionID("session-shell"),
		UserID:        "admin@example.com",
		WorkerID:      "worker-0",
		AccountID:     accountID,
		AgentType:     domain.CloudAgentTypeClaudeCode,
		ChatSessionID: "session-shell",
		WorkspacePath: "/srv/aiyolo/workspace/session-shell",
		Status:        domain.CloudAgentSessionStatusActive,
		CreatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}

	fakeShell := newFakeInteractiveShell("ready$ ")
	handler := NewHandler(Config{
		SecretKey:     "test-secret",
		AdminEmail:    "admin@example.com",
		AdminPassword: "password",
	}, store)
	handler.openCloudAgentShell = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, cols, rows int) (workerops.InteractiveShell, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected shell bridge inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if cols != consoleChatShellCols || rows != consoleChatShellRows {
			t.Fatalf("unexpected default shell size cols=%d rows=%d", cols, rows)
		}
		return fakeShell, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverURL.Path = "/console/chat/shell/ws"
	config, err := websocket.NewConfig("ws"+strings.TrimPrefix(server.URL, "http")+"/console/chat/shell/ws?session=session-shell", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(serverURL) {
		config.Header.Add("Cookie", cookie.String())
	}
	ws, err := websocket.DialConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	if err := websocket.JSON.Send(ws, consoleChatShellSocketRequest{Type: "resize", Cols: 132, Rows: 41}); err != nil {
		t.Fatal(err)
	}
	if resize := fakeShell.waitResize(t); resize[0] != 132 || resize[1] != 41 {
		t.Fatalf("unexpected resize payload: %+v", resize)
	}
	if err := websocket.JSON.Send(ws, consoleChatShellSocketRequest{Type: "input", Data: "pwd\r"}); err != nil {
		t.Fatal(err)
	}
	if write := fakeShell.waitWrite(t); write != "pwd\r" {
		t.Fatalf("unexpected shell input: %q", write)
	}

	var readySeen bool
	var echoSeen bool
	for !(readySeen && echoSeen) {
		var event consoleChatShellSocketEvent
		if err := websocket.JSON.Receive(ws, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "output" {
			continue
		}
		if strings.Contains(event.Data, "ready$") {
			readySeen = true
		}
		if strings.Contains(event.Data, "pwd\r") {
			echoSeen = true
		}
	}
}

func mountedConsoleTestServer(handler *Handler) *httptest.Server {
	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	return httptest.NewServer(router)
}

func seedChatEnvironmentRoute(t *testing.T, ctx context.Context, store *storage.MemoryStore, providerBaseURL string) {
	t.Helper()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: strings.TrimRight(providerBaseURL, "/") + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_env", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 1000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_env", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
}

func seedChatEnvironmentWorker(t *testing.T, ctx context.Context, store *storage.MemoryStore, workerID string) {
	t.Helper()
	if err := store.UpsertWorkerSSHKey(ctx, domain.WorkerSSHKey{ID: "ssh-key-1", Name: "Primary Key", PrivateKey: mustGenerateWorkersPrivateKeyPEM(t)}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkerServer(ctx, domain.WorkerServer{ID: workerID, Name: workerID, SSHHost: "10.0.0.9", SSHPort: 22, SSHUsername: "ubuntu", SSHKeyID: "ssh-key-1", InstallProxyID: domain.ProxyTypeDirect, DataRoot: "/srv/aiyolo"}); err != nil {
		t.Fatal(err)
	}
}

type fakeInteractiveShell struct {
	outputReader *io.PipeReader
	outputWriter *io.PipeWriter
	resizeCh     chan [2]int
	writeCh      chan string
	closeOnce    sync.Once
}

func newFakeInteractiveShell(initialOutput string) *fakeInteractiveShell {
	reader, writer := io.Pipe()
	shell := &fakeInteractiveShell{
		outputReader: reader,
		outputWriter: writer,
		resizeCh:     make(chan [2]int, 4),
		writeCh:      make(chan string, 4),
	}
	if strings.TrimSpace(initialOutput) != "" {
		go func() {
			_, _ = io.WriteString(shell.outputWriter, initialOutput)
		}()
	}
	return shell
}

func (shell *fakeInteractiveShell) Read(payload []byte) (int, error) {
	return shell.outputReader.Read(payload)
}

func (shell *fakeInteractiveShell) Write(payload []byte) (int, error) {
	text := string(payload)
	select {
	case shell.writeCh <- text:
	default:
	}
	go func() {
		_, _ = io.WriteString(shell.outputWriter, text)
	}()
	return len(payload), nil
}

func (shell *fakeInteractiveShell) Resize(cols, rows int) error {
	select {
	case shell.resizeCh <- [2]int{cols, rows}:
	default:
	}
	return nil
}

func (shell *fakeInteractiveShell) Close() error {
	shell.closeOnce.Do(func() {
		_ = shell.outputWriter.Close()
		_ = shell.outputReader.Close()
	})
	return nil
}

func (shell *fakeInteractiveShell) waitResize(t *testing.T) [2]int {
	t.Helper()
	select {
	case resize := <-shell.resizeCh:
		return resize
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shell resize")
	}
	return [2]int{}
}

func (shell *fakeInteractiveShell) waitWrite(t *testing.T) string {
	t.Helper()
	select {
	case write := <-shell.writeCh:
		return write
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shell input")
	}
	return ""
}
