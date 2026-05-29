package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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

	"github.com/zltl/aiyolo/internal/artifacts"
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
		Artifacts:          artifacts.Config{PublicBaseURL: "https://files.example.com"},
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
	if ensured.ASSDownloadURL != "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass" || ensured.ASSSHA256URL != "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass.sha256" {
		t.Fatalf("unexpected ensured aiyolo-ass artifact urls: %+v", ensured)
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

func TestChatEnvironmentEnsureRotatesStaleCloudAgentAPIKey(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "anthropic-main", Name: "Anthropic", BaseURL: "https://anthropic.invalid", Protocol: domain.ProtocolAnthropic, MasterKey: "sk-ant", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "anthropic/claude-opus-4.7", ProviderID: "anthropic-main", UpstreamModel: "anthropic/claude-opus-4.7", Protocol: domain.ProtocolAnthropic, AllowedProtocols: []string{domain.ProtocolAnthropic}, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(-time.Hour)
	oldClearKey, oldAPIKey, err := newConsoleAPIKey(apiKeySpec{
		ID:               "cloud-agent-worker-0-key",
		Name:             "Cloud Agent worker-0",
		Kind:             "live",
		UserID:           "admin@example.com",
		AllowedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic},
		AllowedModels:    []string{"gpt-5.4"},
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, oldAPIKey); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{
		ID:              consoleChatCloudAgentAccountID("worker-0"),
		UserID:          "admin@example.com",
		WorkerID:        "worker-0",
		AgentType:       domain.CloudAgentTypeClaudeCode,
		ModelPublicName: "gpt-5.4",
		ContainerName:   "aiyolo-cloud-agent-worker-0",
		WorkspacePath:   "/srv/aiyolo/workspace/chat-session",
		Credential:      oldClearKey,
		Status:          domain.CloudAgentStatusRunning,
		CreatedAt:       now,
	}); err != nil {
		t.Fatal(err)
	}

	var ensured workerops.CloudAgentStartOptions
	handler := NewHandler(Config{
		SecretKey:          "test-secret",
		AdminEmail:         "admin@example.com",
		AdminPassword:      "password",
		CodexPublicBaseURL: "https://aiyolo.quant67.com",
	}, store)
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensured = options
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
		"chat_public_name": {"anthropic/claude-opus-4.7"},
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

	if ensured.APIKey == oldClearKey {
		t.Fatalf("expected ensure to rotate the stale api key")
	}
	if joined := strings.Join(ensured.AllowedModels, ","); joined != "gpt-5.4,anthropic/claude-opus-4.7" {
		t.Fatalf("unexpected ensured allowed models: %s", joined)
	}

	account, err := store.GetCloudAgentAccount(ctx, "admin@example.com", consoleChatCloudAgentAccountID("worker-0"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(account.Credential) == "" || account.Credential == oldClearKey {
		t.Fatalf("expected account credential to be rotated: %+v", account)
	}
	keys, err := store.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected one updated api key, got %+v", keys)
	}
	if keys[0].ID != "cloud-agent-worker-0-key" {
		t.Fatalf("expected api key id to be preserved, got %+v", keys[0])
	}
	if joined := strings.Join(keys[0].AllowedModels, ","); joined != "gpt-5.4,anthropic/claude-opus-4.7" {
		t.Fatalf("unexpected stored allowed models: %s", joined)
	}
}

func TestChatEnvironmentEnsureRewritesLoopbackBaseURLForWorker(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")
	if err := store.UpsertWorkerServer(ctx, domain.WorkerServer{ID: "worker-0", Name: "worker-0", SSHHost: "172.22.113.86", SSHPort: 22, SSHUsername: "ubuntu", SSHKeyID: "ssh-key-1", InstallProxyID: domain.ProxyTypeDirect, DataRoot: "/srv/aiyolo"}); err != nil {
		t.Fatal(err)
	}

	var ensured workerops.CloudAgentStartOptions
	handler := NewHandler(Config{
		SecretKey:     "test-secret",
		AdminEmail:    "admin@example.com",
		AdminPassword: "password",
		ChatAttachments: artifacts.Config{
			ProxyBasePath: "/console/chat/attachments/files",
			S3: artifacts.S3Config{
				Endpoint:        "https://s3.example.com",
				Bucket:          "aiyolo-chat-assets",
				Prefix:          "chat",
				AccessKeyID:     "key",
				AccessKeySecret: "secret",
			},
		},
	}, store)
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensured = options
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

	expectedConsoleURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	expectedConsoleURL.Host = net.JoinHostPort("172.22.113.86", expectedConsoleURL.Port())
	expectedConsole := strings.TrimRight(expectedConsoleURL.String(), "/")
	if ensured.ConsoleBaseURL != expectedConsole || ensured.APIBaseURL != expectedConsole+"/v1" {
		t.Fatalf("unexpected rewritten base urls api=%q console=%q", ensured.APIBaseURL, ensured.ConsoleBaseURL)
	}
}

func TestSendChatEnsuresCloudAgentEnvironment(t *testing.T) {
	var ensureCalls atomic.Int32
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	var cloudChatCalls atomic.Int32
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
	handler.runCloudAgentChat = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		cloudChatCalls.Add(1)
		if ensureCalls.Load() == 0 {
			t.Fatal("cloud agent chat happened before cloud agent ensure")
		}
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || session.ChatSessionID != "session-send-env" {
			t.Fatalf("unexpected cloud chat target worker=%+v key=%+v session=%+v", worker, key, session)
		}
		return consoleChatExecution{
			Result: consoleChatResultView{
				PublicName:    request.PublicName,
				ProviderID:    "cloud-agent:worker-0",
				ProviderName:  "Claude Code · worker-0",
				UpstreamModel: request.PublicName,
				Output:        "Cloud container is ready.",
				ResponseID:    "claude-session-send",
			},
			StatusCode: http.StatusOK,
			Usage:      domain.UsageRecord{Currency: "USD", StatusCode: http.StatusOK},
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
	if ensureCalls.Load() != 1 || cloudChatCalls.Load() != 1 {
		t.Fatalf("ensure calls=%d cloud_chat_calls=%d", ensureCalls.Load(), cloudChatCalls.Load())
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
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	var cloudChatCalls atomic.Int32
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
	handler.runCloudAgentChat = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		cloudChatCalls.Add(1)
		if ensureCalls.Load() == 0 {
			t.Fatal("cloud agent stream happened before cloud agent ensure")
		}
		if !request.Stream || request.OnDelta == nil {
			t.Fatalf("expected cloud agent streaming request: %+v", request)
		}
		if err := request.OnDelta("Cloud "); err != nil {
			return consoleChatExecution{}, err
		}
		if err := request.OnDelta("agent is live."); err != nil {
			return consoleChatExecution{}, err
		}
		return consoleChatExecution{
			Result: consoleChatResultView{
				PublicName:    request.PublicName,
				ProviderID:    "cloud-agent:worker-0",
				ProviderName:  "Claude Code · worker-0",
				UpstreamModel: request.PublicName,
				Output:        "Cloud agent is live.",
				ResponseID:    "claude-session-stream",
			},
			StatusCode: http.StatusOK,
			Usage:      domain.UsageRecord{Currency: "USD", StatusCode: http.StatusOK, Stream: true},
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
	if ensureCalls.Load() != 1 || cloudChatCalls.Load() != 1 {
		t.Fatalf("ensure calls=%d cloud_chat_calls=%d", ensureCalls.Load(), cloudChatCalls.Load())
	}
	session, err := store.GetCloudAgentSession(ctx, "admin@example.com", consoleChatCloudAgentSessionID("session-stream-env"))
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.CloudAgentSessionStatusActive || session.WorkerID != "worker-0" {
		t.Fatalf("unexpected cloud agent session after stream: %+v", session)
	}
}

func TestSendChatRoutesThroughCloudAgentClaudeCode(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	var ensureCalls atomic.Int32
	var cloudChatCalls atomic.Int32
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
		if options.DefaultModel != "gpt-5.4" {
			t.Fatalf("unexpected default model: %+v", options)
		}
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-send-claude",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/session-claude-send",
		}, nil
	}
	handler.runCloudAgentChat = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		cloudChatCalls.Add(1)
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" {
			t.Fatalf("unexpected cloud chat target worker=%+v key=%+v", worker, key)
		}
		if account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-claude-send" {
			t.Fatalf("unexpected cloud chat account/session account=%+v session=%+v", account, session)
		}
		if request.PublicName != "gpt-5.4" || request.UserInput != "请直接让 Claude Code 帮我看这个仓库" || request.Stream {
			t.Fatalf("unexpected cloud chat request: %+v", request)
		}
		return consoleChatExecution{
			Result: consoleChatResultView{
				PublicName:    "gpt-5.4",
				ProviderID:    "cloud-agent:worker-0",
				ProviderName:  "Claude Code · worker-0",
				UpstreamModel: "gpt-5.4",
				Output:        "Claude Code 已接管当前 Cloud Agent，会在容器里继续工作。",
				ResponseID:    "claude-session-send",
			},
			StatusCode: http.StatusOK,
			Usage:      domain.UsageRecord{Currency: "USD", StatusCode: http.StatusOK},
		}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat", strings.NewReader(url.Values{
		"chat_client_session_id": {"session-claude-send"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
		"chat_draft":             {"请直接让 Claude Code 帮我看这个仓库"},
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
	if !strings.Contains(string(body), "Claude Code 已接管当前 Cloud Agent") {
		t.Fatalf("assistant output missing from cloud agent send response: %s", body)
	}
	if ensureCalls.Load() != 1 || cloudChatCalls.Load() != 1 {
		t.Fatalf("ensure_calls=%d cloud_chat_calls=%d", ensureCalls.Load(), cloudChatCalls.Load())
	}
}

func TestStreamChatRoutesThroughCloudAgentClaudeCode(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

	var ensureCalls atomic.Int32
	var cloudChatCalls atomic.Int32
	handler := NewHandler(Config{
		SecretKey:          "test-secret",
		AdminEmail:         "admin@example.com",
		AdminPassword:      "password",
		CodexPublicBaseURL: "https://aiyolo.quant67.com",
	}, store)
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-stream-claude",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/session-claude-stream",
		}, nil
	}
	handler.runCloudAgentChat = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		cloudChatCalls.Add(1)
		if !request.Stream || request.OnDelta == nil {
			t.Fatalf("expected stream request with delta callback: %+v", request)
		}
		if err := request.OnDelta("Claude "); err != nil {
			return consoleChatExecution{}, err
		}
		if err := request.OnDelta("Code 已经开始处理。"); err != nil {
			return consoleChatExecution{}, err
		}
		return consoleChatExecution{
			Result: consoleChatResultView{
				PublicName:    "gpt-5.4",
				ProviderID:    "cloud-agent:worker-0",
				ProviderName:  "Claude Code · worker-0",
				UpstreamModel: "gpt-5.4",
				Output:        "Claude Code 已经开始处理。",
				ResponseID:    "claude-session-stream",
			},
			StatusCode: http.StatusOK,
			Usage:      domain.UsageRecord{Currency: "USD", StatusCode: http.StatusOK, Stream: true},
		}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", strings.NewReader(url.Values{
		"chat_client_session_id": {"session-claude-stream"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
		"chat_draft":             {"请让 Claude Code 直接处理这个任务"},
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
	if !strings.Contains(text, `"type":"delta","delta":"Claude "`) || !strings.Contains(text, `"type":"done"`) || !strings.Contains(text, "Claude Code 已经开始处理。") {
		t.Fatalf("unexpected cloud agent stream body: %s", text)
	}
	if ensureCalls.Load() != 1 || cloudChatCalls.Load() != 1 {
		t.Fatalf("ensure_calls=%d cloud_chat_calls=%d", ensureCalls.Load(), cloudChatCalls.Load())
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
		ChatAttachments: artifacts.Config{
			ProxyBasePath: "/console/chat/attachments/files",
			S3: artifacts.S3Config{
				Endpoint:        "https://s3.example.com",
				Bucket:          "aiyolo-chat-assets",
				Prefix:          "chat",
				AccessKeyID:     "key",
				AccessKeySecret: "secret",
			},
		},
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
	if !strings.Contains(html, "data-chat-shell-socket-url=\"/console/chat/shell/ws\"") {
		t.Fatalf("chat page is missing embedded shell socket wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-attachment-tree-url=\"/console/chat/attachments/tree\"") {
		t.Fatalf("chat page is missing attachment tree wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-attachment-tree-enabled=\"true\"") {
		t.Fatalf("chat page did not enable attachment tree browsing: %s", html)
	}
	if !strings.Contains(html, "data-chat-workspace-tree-url=\"/console/chat/workspace/tree\"") {
		t.Fatalf("chat page is missing workspace tree wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-workspace-file-url=\"/console/chat/workspace/file\"") {
		t.Fatalf("chat page is missing workspace file wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-action=\"open-shell\"") {
		t.Fatalf("chat page did not render the shell launch button: %s", html)
	}
	for _, marker := range []string{
		"class=\"chat-activitybar\"",
		"class=\"chat-activitybar-group chat-activitybar-group-panels\"",
		"data-chat-action=\"switch-sidebar-view\" data-chat-sidebar-view=\"files\"",
		"data-chat-action=\"switch-sidebar-view\" data-chat-sidebar-view=\"sessions\"",
		"data-chat-action=\"toggle-pane\" data-chat-pane=\"sidebar\"",
		"data-chat-action=\"toggle-pane\" data-chat-pane=\"editor\"",
		"data-chat-action=\"toggle-pane\" data-chat-pane=\"chat\"",
		"data-chat-attachment-tree",
		"data-chat-workspace-section",
		"data-chat-workspace-tree",
		"data-chat-editor-preview",
		"data-chat-action=\"set-image-preview-background\" data-chat-preview-background=\"grid\"",
		"data-chat-action=\"set-image-preview-background\" data-chat-preview-background=\"light\"",
		"data-chat-action=\"set-image-preview-background\" data-chat-preview-background=\"dark\"",
		"data-chat-editor-input",
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("chat page did not render workbench marker %s: %s", marker, html)
		}
	}
	if !strings.Contains(html, "data-chat-shell-dock") || !strings.Contains(html, "data-chat-shell-tabs") {
		t.Fatalf("chat page did not render the embedded shell dock: %s", html)
	}
	for _, action := range []string{
		"data-chat-shell-action=\"new\"",
		"data-chat-shell-action=\"clear\"",
		"data-chat-shell-action=\"reconnect\"",
		"data-chat-shell-action=\"close\"",
	} {
		if strings.Contains(html, action) {
			t.Fatalf("chat page should not render shell dock action %s: %s", action, html)
		}
	}
	if footerIndex, dockIndex := strings.Index(html, "class=\"chat-footer\""), strings.Index(html, "data-chat-shell-dock"); footerIndex < 0 || dockIndex < 0 || dockIndex < footerIndex {
		t.Fatalf("chat page should render the shell dock below the composer footer: %s", html)
	}
}

func TestChatPageHidesShellLaunchButtonForLocalChat(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")

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
	response, err := client.Get(server.URL + "/console/chat")
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
	if !strings.Contains(html, "name=\"chat_environment\" value=\"local\" data-chat-environment-input") {
		t.Fatalf("chat page should default to local chat: %s", html)
	}
	if !strings.Contains(html, "data-chat-action=\"open-shell\" hidden disabled") {
		t.Fatalf("chat page should hide the shell launch button for local chat: %s", html)
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

func TestChatShellReadyEndpointLoadsCloudAgentSession(t *testing.T) {
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
	response, err := client.Get(server.URL + "/console/chat/shell/ready?session=session-shell")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat shell ready status=%d body=%s", response.StatusCode, body)
	}
	var payload consoleChatShellReadyResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ready" || payload.SessionID != "session-shell" || payload.Environment != consoleChatEnvironmentValue("worker-0") {
		t.Fatalf("unexpected shell ready payload: %+v", payload)
	}
	if payload.WorkerID != "worker-0" || payload.ContainerName != "aiyolo-cloud-agent-worker-0" || payload.WorkspacePath != "/srv/aiyolo/workspace/session-shell" {
		t.Fatalf("unexpected shell metadata: %+v", payload)
	}
	if payload.SocketURL != "/console/chat/shell/ws?session=session-shell" {
		t.Fatalf("unexpected shell socket url: %+v", payload)
	}
}

func TestChatWorkspaceTreeEndpointLoadsCloudAgentSession(t *testing.T) {
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
	var ensureCalls atomic.Int32
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		t.Fatalf("workspace tree should use the active session fast path without ensuring runtime worker=%+v key=%+v proxy=%+v options=%+v", worker, key, proxy, options)
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-workspace-tree",
			ContainerName: options.ContainerName,
			WorkspacePath: options.WorkspacePath,
		}, nil
	}
	handler.listCloudAgentWorkspaceTree = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string) (workerops.CloudAgentWorkspaceTree, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace tree bridge inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if ensureCalls.Load() != 0 {
			t.Fatal("workspace tree command should not run a full runtime ensure on the fast path")
		}
		if relativePath != "" {
			t.Fatalf("workspace tree did not target the workspace root: %q", relativePath)
		}
		return workerops.CloudAgentWorkspaceTree{
			Path:    "",
			Entries: []workerops.CloudAgentWorkspaceEntry{{Name: "cmd", Path: "cmd", Type: "directory", HasChildren: true}, {Name: "README.md", Path: "README.md", Type: "file", Size: 128, ModifiedAt: "2026-05-27T03:10:00Z"}},
			Children: map[string][]workerops.CloudAgentWorkspaceEntry{
				"cmd": {{Name: "main.go", Path: "cmd/main.go", Type: "file", Size: 64}},
			},
		}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL + "/console/chat/workspace/tree?session=session-shell")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat workspace tree status=%d body=%s", response.StatusCode, body)
	}
	var payload consoleChatWorkspaceTreeResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ready" || payload.SessionID != "session-shell" || payload.Environment != consoleChatEnvironmentValue("worker-0") {
		t.Fatalf("unexpected workspace tree payload: %+v", payload)
	}
	if payload.WorkerID != "worker-0" || payload.ContainerName != "aiyolo-cloud-agent-worker-0" || payload.WorkspacePath != "/srv/aiyolo/workspace/session-shell" {
		t.Fatalf("unexpected workspace tree metadata: %+v", payload)
	}
	if len(payload.Entries) != 2 || payload.Entries[0].Name != "cmd" || payload.Entries[1].Path != "README.md" {
		t.Fatalf("unexpected workspace tree entries: %+v", payload.Entries)
	}
	if ensureCalls.Load() != 0 {
		t.Fatalf("ensure calls=%d, want 0", ensureCalls.Load())
	}
	if len(payload.Children["cmd"]) != 1 || payload.Children["cmd"][0].Path != "cmd/main.go" {
		t.Fatalf("unexpected prefetched workspace children: %+v", payload.Children)
	}
}

func TestChatWorkspaceTreeRestoresRuntimeWhenBridgeContainerMissing(t *testing.T) {
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
	var ensureCalls atomic.Int32
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || proxy.ID != domain.ProxyTypeDirect {
			t.Fatalf("unexpected workspace restore inputs worker=%+v key=%+v proxy=%+v", worker, key, proxy)
		}
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-workspace-restored",
			ContainerName: options.ContainerName,
			WorkspacePath: options.WorkspacePath,
		}, nil
	}
	var listCalls atomic.Int32
	handler.listCloudAgentWorkspaceTree = func(_ context.Context, _ domain.WorkerServer, _ domain.WorkerSSHKey, _ domain.CloudAgentAccount, _ domain.CloudAgentSession, relativePath string) (workerops.CloudAgentWorkspaceTree, error) {
		if relativePath != "" {
			t.Fatalf("workspace tree did not target the workspace root: %q", relativePath)
		}
		if listCalls.Add(1) == 1 {
			return workerops.CloudAgentWorkspaceTree{}, errors.New("call aiyolo-ass: cloud agent container aiyolo-cloud-agent-worker-0 is not available")
		}
		if ensureCalls.Load() != 1 {
			t.Fatalf("workspace restore should ensure runtime once before retry, got %d", ensureCalls.Load())
		}
		return workerops.CloudAgentWorkspaceTree{Path: "", Entries: []workerops.CloudAgentWorkspaceEntry{{Name: "README.md", Path: "README.md", Type: "file", Size: 128}}}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL + "/console/chat/workspace/tree?session=session-shell")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat workspace tree status=%d body=%s", response.StatusCode, body)
	}
	var payload consoleChatWorkspaceTreeResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ready" || len(payload.Entries) != 1 || payload.Entries[0].Path != "README.md" {
		t.Fatalf("unexpected restored workspace tree payload: %+v", payload)
	}
	if ensureCalls.Load() != 1 || listCalls.Load() != 2 {
		t.Fatalf("ensure calls=%d list calls=%d, want 1 and 2", ensureCalls.Load(), listCalls.Load())
	}
}

func TestChatWorkspaceFileEndpointReadsFile(t *testing.T) {
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
	var ensureCalls atomic.Int32
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		t.Fatalf("workspace file read should use the active session fast path without ensuring runtime worker=%+v key=%+v proxy=%+v options=%+v", worker, key, proxy, options)
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-workspace-file",
			ContainerName: options.ContainerName,
			WorkspacePath: options.WorkspacePath,
		}, nil
	}
	handler.readCloudAgentWorkspaceFile = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string) (workerops.CloudAgentWorkspaceFile, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace file bridge inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if ensureCalls.Load() != 0 {
			t.Fatal("workspace file command should not run a full runtime ensure on the fast path")
		}
		if relativePath != "README.md" {
			t.Fatalf("workspace file did not target README.md: %q", relativePath)
		}
		return workerops.CloudAgentWorkspaceFile{Path: "README.md", Size: 42, Kind: "text", MediaType: "text/plain", Content: "hello from workspace\n"}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL + "/console/chat/workspace/file?session=session-shell&path=README.md")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat workspace file status=%d body=%s", response.StatusCode, body)
	}
	var payload consoleChatWorkspaceFileResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ready" || payload.Path != "README.md" || payload.Size != 42 {
		t.Fatalf("unexpected workspace file payload: %+v", payload)
	}
	if payload.Kind != "text" || payload.MediaType != "text/plain" {
		t.Fatalf("unexpected workspace file metadata: %+v", payload)
	}
	if payload.Content != "hello from workspace\n" {
		t.Fatalf("unexpected workspace file content: %+v", payload)
	}
	if ensureCalls.Load() != 0 {
		t.Fatalf("ensure calls=%d, want 0", ensureCalls.Load())
	}
}

func TestChatWorkspaceFileEndpointReturnsImagePreview(t *testing.T) {
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
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-workspace-file",
			ContainerName: options.ContainerName,
			WorkspacePath: options.WorkspacePath,
		}, nil
	}
	handler.readCloudAgentWorkspaceFile = func(_ context.Context, _ domain.WorkerServer, _ domain.WorkerSSHKey, _ domain.CloudAgentAccount, _ domain.CloudAgentSession, relativePath string) (workerops.CloudAgentWorkspaceFile, error) {
		if relativePath != "assets/diagram.png" {
			t.Fatalf("workspace file did not target assets/diagram.png: %q", relativePath)
		}
		return workerops.CloudAgentWorkspaceFile{
			Path:       "assets/diagram.png",
			Size:       128,
			Kind:       "image",
			MediaType:  "image/png",
			PreviewURL: "data:image/png;base64,cG5n",
		}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL + "/console/chat/workspace/file?session=session-shell&path=assets/diagram.png")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat workspace image status=%d body=%s", response.StatusCode, body)
	}
	var payload consoleChatWorkspaceFileResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ready" || payload.Path != "assets/diagram.png" || payload.Kind != "image" || payload.MediaType != "image/png" {
		t.Fatalf("unexpected workspace image payload: %+v", payload)
	}
	if payload.PreviewURL != "data:image/png;base64,cG5n" || payload.Content != "" {
		t.Fatalf("unexpected workspace image preview data: %+v", payload)
	}
}

func TestSaveChatWorkspaceFilePersistsChanges(t *testing.T) {
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
	var ensureCalls atomic.Int32
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		t.Fatalf("workspace save should use the active session fast path without ensuring runtime worker=%+v key=%+v proxy=%+v options=%+v", worker, key, proxy, options)
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-workspace-save",
			ContainerName: options.ContainerName,
			WorkspacePath: options.WorkspacePath,
		}, nil
	}
	handler.writeCloudAgentWorkspaceFile = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string, content string) (workerops.CloudAgentWorkspaceFile, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace save bridge inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if ensureCalls.Load() != 0 {
			t.Fatal("workspace save command should not run a full runtime ensure on the fast path")
		}
		if relativePath != "cmd/main.go" {
			t.Fatalf("workspace save did not target cmd/main.go: %q", relativePath)
		}
		if content != "package main\n" {
			t.Fatalf("workspace save content mismatch: %q", content)
		}
		return workerops.CloudAgentWorkspaceFile{Path: "cmd/main.go", Bytes: 13}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	requestBody := strings.NewReader(`{"path":"cmd/main.go","content":"package main\n"}`)
	response, err := client.Post(server.URL+"/console/chat/workspace/file?session=session-shell", "application/json", requestBody)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat workspace save status=%d body=%s", response.StatusCode, body)
	}
	var payload consoleChatWorkspaceFileResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "saved" || payload.Path != "cmd/main.go" || payload.Bytes != 13 {
		t.Fatalf("unexpected workspace save payload: %+v", payload)
	}
	if payload.Notice != "文件已保存。" {
		t.Fatalf("unexpected workspace save notice: %+v", payload)
	}
	if ensureCalls.Load() != 0 {
		t.Fatalf("ensure calls=%d, want 0", ensureCalls.Load())
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
	var ensureCalls atomic.Int32
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || proxy.ID != domain.ProxyTypeDirect {
			t.Fatalf("unexpected shell ensure inputs worker=%+v key=%+v proxy=%+v", worker, key, proxy)
		}
		if options.ContainerName != "aiyolo-cloud-agent-worker-0" || options.WorkspacePath != "/srv/aiyolo/workspace/session-shell" {
			t.Fatalf("unexpected shell ensure options: %+v", options)
		}
		if !strings.Contains(options.OpenURL, "/console/chat?session=session-shell") {
			t.Fatalf("unexpected shell ensure open url: %s", options.OpenURL)
		}
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-shell",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: options.WorkspacePath,
		}, nil
	}
	handler.openCloudAgentShell = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, cols, rows int) (workerops.InteractiveShell, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected shell bridge inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if ensureCalls.Load() == 0 {
			t.Fatal("interactive shell opened before ensuring cloud agent runtime")
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
	if ensureCalls.Load() != 1 {
		t.Fatalf("ensure calls=%d, want 1", ensureCalls.Load())
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
