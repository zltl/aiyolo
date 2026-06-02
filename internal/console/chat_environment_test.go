package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
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
	if ensured.DefaultModel != "gpt-5.4" {
		t.Fatalf("unexpected ensured model options: %+v", ensured)
	}
	expectedAllowedModels := []string{"gpt-5.4", "openai/gpt-image-2", "gpt-image-2", "chatgpt-image-2", "black-forest-labs/flux-1.1-pro-ultra"}
	if !consoleChatSameStringSet(ensured.AllowedModels, expectedAllowedModels) {
		t.Fatalf("unexpected ensured allowed models: %+v", ensured.AllowedModels)
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
	if len(keys) != 1 {
		t.Fatalf("unexpected api keys: %+v", keys)
	}
	if !consoleChatSameStringSet(keys[0].AllowedModels, expectedAllowedModels) {
		t.Fatalf("unexpected stored allowed models: %+v", keys[0].AllowedModels)
	}
}

func TestChatEnvironmentEnsureReconcilesStaleCloudAgentAPIKey(t *testing.T) {
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

	if ensured.APIKey != oldClearKey {
		t.Fatalf("expected ensure to keep reconciled api key credential")
	}
	expectedAllowedModels := []string{"gpt-5.4", "openai/gpt-image-2", "gpt-image-2", "chatgpt-image-2", "black-forest-labs/flux-1.1-pro-ultra"}
	if !consoleChatSameStringSet(ensured.AllowedModels, expectedAllowedModels) {
		t.Fatalf("unexpected ensured allowed models: %+v", ensured.AllowedModels)
	}

	account, err := store.GetCloudAgentAccount(ctx, "admin@example.com", consoleChatCloudAgentAccountID("worker-0"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(account.Credential) == "" || account.Credential != oldClearKey {
		t.Fatalf("expected account credential to stay on reconciled key: %+v", account)
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
	if !consoleChatSameStringSet(keys[0].AllowedModels, expectedAllowedModels) {
		t.Fatalf("unexpected stored allowed models: %+v", keys[0].AllowedModels)
	}
}

func TestChatEnvironmentEnsureReusesActiveCloudAgentSessionAfterOldLastSeen(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")
	oldSeen := time.Now().UTC().Add(-6 * time.Hour)
	accountID := consoleChatCloudAgentAccountID("worker-0")
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{
		ID:              accountID,
		UserID:          "admin@example.com",
		WorkerID:        "worker-0",
		AgentType:       domain.CloudAgentTypeClaudeCode,
		ModelPublicName: "gpt-5.4",
		ContainerID:     "container-123",
		ContainerName:   "aiyolo-cloud-agent-worker-0",
		WorkspacePath:   "/srv/aiyolo/workspace/chat-session",
		Credential:      "sk-cloud-agent",
		Status:          domain.CloudAgentStatusRunning,
		CreatedAt:       oldSeen,
		LastStartedAt:   &oldSeen,
		LastSeenAt:      &oldSeen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentSession(ctx, domain.CloudAgentSession{
		ID:            consoleChatCloudAgentSessionID("session-refresh"),
		UserID:        "admin@example.com",
		WorkerID:      "worker-0",
		AccountID:     accountID,
		AgentType:     domain.CloudAgentTypeClaudeCode,
		ChatSessionID: "session-refresh",
		WorkspacePath: "/srv/aiyolo/workspace/chat-session",
		Status:        domain.CloudAgentSessionStatusActive,
	}); err != nil {
		t.Fatal(err)
	}

	var ensureCalls atomic.Int32
	handler := NewHandler(Config{
		SecretKey:          "test-secret",
		AdminEmail:         "admin@example.com",
		AdminPassword:      "password",
		CodexPublicBaseURL: "https://aiyolo.quant67.com",
	}, store)
	handler.ensureCloudAgent = func(_ context.Context, _ domain.WorkerServer, _ domain.WorkerSSHKey, _ domain.ProxyProfile, _ workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		return workerops.CloudAgentInstance{}, errors.New("refresh should reuse the active cloud agent session")
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.PostForm(server.URL+"/console/chat/environment/ensure", url.Values{
		"chat_client_session_id": {"session-refresh"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("ensure status=%d body=%s", response.StatusCode, body)
	}
	if ensureCalls.Load() != 0 {
		t.Fatalf("ensure calls=%d, want reused active session", ensureCalls.Load())
	}
	account, err := store.GetCloudAgentAccount(ctx, "admin@example.com", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if account.LastSeenAt == nil || !account.LastSeenAt.After(oldSeen) {
		t.Fatalf("expected reuse to refresh last seen timestamp: %+v", account)
	}
}

func TestChatEnvironmentEnsureLiveKeyIncludesGPTImage2Aliases(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_img2_env", ModelAlias: "chatgpt-image-2", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 1000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "chatgpt-image-2", ProviderID: "openrouter", UpstreamModel: "chatgpt-image-2", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_img2_env", Enabled: true, Priority: 1, Weight: 100}); err != nil {
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
		"chat_public_name": {"chatgpt-image-2"},
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

	expectedModels := []string{"gpt-5.4", "chatgpt-image-2", "gpt-image-2", "openai/gpt-image-2", "black-forest-labs/flux-1.1-pro-ultra"}
	if !consoleChatSameStringSet(ensured.AllowedModels, expectedModels) {
		t.Fatalf("unexpected ensured allowed models: %+v", ensured.AllowedModels)
	}

	keys, err := store.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected one api key, got %+v", keys)
	}
	if !consoleChatSameStringSet(keys[0].AllowedModels, expectedModels) {
		t.Fatalf("unexpected stored allowed models: %+v", keys[0].AllowedModels)
	}
}

func TestConsoleChatExpandAllowedModelsIncludesPreferredImageModels(t *testing.T) {
	expanded := consoleChatExpandAllowedModels([]string{"gpt-5.4"})
	expected := []string{"gpt-5.4", "openai/gpt-image-2", "gpt-image-2", "chatgpt-image-2", "black-forest-labs/flux-1.1-pro-ultra"}
	if !consoleChatSameStringSet(expanded, expected) {
		t.Fatalf("unexpected expanded allowed models: %+v", expanded)
	}
}

func TestChatPageFiltersRoutesByEffectiveAPIAllowedModels(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	seedChatEnvironmentRoute(t, ctx, store, "https://provider.invalid")
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_img2_filter", ModelAlias: "chatgpt-image-2", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 1000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gemini_filter", ModelAlias: "google/gemini-3.1-pro-preview", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 1000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "chatgpt-image-2", ProviderID: "openrouter", UpstreamModel: "chatgpt-image-2", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_img2_filter", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "google/gemini-3.1-pro-preview", ProviderID: "openrouter", UpstreamModel: "google/gemini-3.1-pro-preview", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gemini_filter", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	clearKey, apiKey, err := newConsoleAPIKey(apiKeySpec{
		ID:               "cloud-agent-worker-0-key",
		Name:             "Cloud Agent worker-0",
		Kind:             "live",
		UserID:           "admin@example.com",
		Status:           domain.StatusActive,
		AllowedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic},
		AllowedModels:    []string{"gpt-5.4", "gpt-image-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{ID: consoleChatCloudAgentAccountID("worker-0"), UserID: "admin@example.com", WorkerID: "worker-0", AgentType: domain.CloudAgentTypeClaudeCode, Credential: clearKey, Status: domain.CloudAgentStatusRunning, WorkspacePath: domain.DefaultCloudAgentWorkspacePath}); err != nil {
		t.Fatal(err)
	}

	server := mountedConsoleTestServer(NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store))
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
	for _, expected := range []string{"gpt-5.4", "chatgpt-image-2"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("expected API-allowed model %q in chat page: %s", expected, html)
		}
	}
	if strings.Contains(html, "google/gemini-3.1-pro-preview") {
		t.Fatalf("unexpected non-API-allowed model in chat page: %s", html)
	}
}

func TestChatPageAutoHealsCloudAgentAPIKeyPreferredImageModels(t *testing.T) {
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
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "anthropic/claude-opus-4.8", ProviderID: "anthropic-main", UpstreamModel: "anthropic/claude-opus-4.8", Protocol: domain.ProtocolAnthropic, AllowedProtocols: []string{domain.ProtocolAnthropic}, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	clearKey, apiKey, err := newConsoleAPIKey(apiKeySpec{
		ID:               "cloud-agent-worker-0-key",
		Name:             "Cloud Agent worker-0",
		Kind:             "live",
		UserID:           "admin@example.com",
		Status:           domain.StatusActive,
		AllowedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic},
		AllowedModels:    []string{"deepseek-v4-pro", "anthropic/claude-opus-4.8", "openai/gpt-5.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{ID: consoleChatCloudAgentAccountID("worker-0"), UserID: "admin@example.com", WorkerID: "worker-0", AgentType: domain.CloudAgentTypeClaudeCode, Credential: clearKey, Status: domain.CloudAgentStatusRunning, WorkspacePath: domain.DefaultCloudAgentWorkspacePath}); err != nil {
		t.Fatal(err)
	}

	server := mountedConsoleTestServer(NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store))
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

	keys, err := store.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected one api key, got %+v", keys)
	}
	expectedModels := []string{"deepseek-v4-pro", "anthropic/claude-opus-4.8", "openai/gpt-5.5", "openai/gpt-image-2", "gpt-image-2", "chatgpt-image-2", "black-forest-labs/flux-1.1-pro-ultra"}
	if !consoleChatSameStringSet(keys[0].AllowedModels, expectedModels) {
		t.Fatalf("unexpected auto-healed allowed models: %+v", keys[0].AllowedModels)
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
				ProviderName:  "Codex · worker-0",
				UpstreamModel: request.PublicName,
				Output:        "Cloud container is ready.",
				ResponseID:    "codex-thread-send",
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
				ProviderName:  "Codex · worker-0",
				UpstreamModel: request.PublicName,
				Output:        "Cloud agent is live.",
				ResponseID:    "codex-thread-stream",
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

func TestStreamChatReusesRecentlyEnsuredCloudAgentEnvironment(t *testing.T) {
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
	handler.ensureCloudAgent = func(_ context.Context, worker domain.WorkerServer, _ domain.WorkerSSHKey, _ domain.ProxyProfile, _ workerops.CloudAgentStartOptions) (workerops.CloudAgentInstance, error) {
		ensureCalls.Add(1)
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-stream-reuse",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/session-stream-reuse",
		}, nil
	}
	handler.runCloudAgentChat = func(_ context.Context, _ domain.WorkerServer, _ domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		cloudChatCalls.Add(1)
		if account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-stream-reuse" {
			t.Fatalf("unexpected cloud chat target account=%+v session=%+v", account, session)
		}
		if err := request.OnDelta("reused cloud agent"); err != nil {
			return consoleChatExecution{}, err
		}
		return consoleChatExecution{
			Result: consoleChatResultView{
				PublicName:    request.PublicName,
				ProviderID:    "cloud-agent:worker-0",
				ProviderName:  "Codex · worker-0",
				UpstreamModel: request.PublicName,
				Output:        "reused cloud agent",
				ResponseID:    "codex-thread-stream-reuse",
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

	ensureResponse, err := client.PostForm(server.URL+"/console/chat/environment/ensure", url.Values{
		"chat_client_session_id": {"session-stream-reuse"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ensureResponse.Body.Close()
	if ensureResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(ensureResponse.Body)
		t.Fatalf("ensure status=%d body=%s", ensureResponse.StatusCode, body)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", strings.NewReader(url.Values{
		"chat_client_session_id": {"session-stream-reuse"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
		"chat_draft":             {"reuse the already started cloud agent"},
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
	if text := string(body); !strings.Contains(text, "reused cloud agent") || !strings.Contains(text, `"type":"done"`) {
		t.Fatalf("unexpected stream body: %s", text)
	}
	if ensureCalls.Load() != 1 || cloudChatCalls.Load() != 1 {
		t.Fatalf("ensure_calls=%d cloud_chat_calls=%d", ensureCalls.Load(), cloudChatCalls.Load())
	}
}

func TestSendChatRoutesThroughCloudAgentCodex(t *testing.T) {
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
			ContainerID:   "container-send-codex",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/session-codex-send",
		}, nil
	}
	handler.runCloudAgentChat = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		cloudChatCalls.Add(1)
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" {
			t.Fatalf("unexpected cloud chat target worker=%+v key=%+v", worker, key)
		}
		if account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-codex-send" {
			t.Fatalf("unexpected cloud chat account/session account=%+v session=%+v", account, session)
		}
		if request.PublicName != "gpt-5.4" || request.UserInput != "请直接让 Codex 帮我看这个仓库" || request.Stream {
			t.Fatalf("unexpected cloud chat request: %+v", request)
		}
		return consoleChatExecution{
			Result: consoleChatResultView{
				PublicName:    "gpt-5.4",
				ProviderID:    "cloud-agent:worker-0",
				ProviderName:  "Codex · worker-0",
				UpstreamModel: "gpt-5.4",
				Output:        "Codex 已接管当前 Cloud Agent，会在容器里继续工作。",
				ResponseID:    "codex-thread-send",
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
		"chat_client_session_id": {"session-codex-send"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
		"chat_draft":             {"请直接让 Codex 帮我看这个仓库"},
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
	if !strings.Contains(string(body), "Codex 已接管当前 Cloud Agent") {
		t.Fatalf("assistant output missing from cloud agent send response: %s", body)
	}
	if ensureCalls.Load() != 1 || cloudChatCalls.Load() != 1 {
		t.Fatalf("ensure_calls=%d cloud_chat_calls=%d", ensureCalls.Load(), cloudChatCalls.Load())
	}
}

func TestStreamChatRoutesThroughCloudAgentCodex(t *testing.T) {
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
			ContainerID:   "container-stream-codex",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: "/srv/aiyolo/workspace/session-codex-stream",
		}, nil
	}
	handler.runCloudAgentChat = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		cloudChatCalls.Add(1)
		if !request.Stream || request.OnDelta == nil {
			t.Fatalf("expected stream request with delta callback: %+v", request)
		}
		if err := request.OnDelta("Codex "); err != nil {
			return consoleChatExecution{}, err
		}
		if err := request.OnDelta("已经开始处理。"); err != nil {
			return consoleChatExecution{}, err
		}
		return consoleChatExecution{
			Result: consoleChatResultView{
				PublicName:    "gpt-5.4",
				ProviderID:    "cloud-agent:worker-0",
				ProviderName:  "Codex · worker-0",
				UpstreamModel: "gpt-5.4",
				Output:        "Codex 已经开始处理。",
				ResponseID:    "codex-thread-stream",
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
		"chat_client_session_id": {"session-codex-stream"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_environment":       {consoleChatEnvironmentValue("worker-0")},
		"chat_draft":             {"请让 Codex 直接处理这个任务"},
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
	if !strings.Contains(text, `"type":"delta","delta":"Codex "`) || !strings.Contains(text, `"type":"done"`) || !strings.Contains(text, "Codex 已经开始处理。") {
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
	if !strings.Contains(html, "data-chat-workspace-download-url=\"/console/chat/workspace/download\"") {
		t.Fatalf("chat page is missing workspace download wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-workspace-copy-url=\"/console/chat/workspace/copy\"") {
		t.Fatalf("chat page is missing workspace copy wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-workspace-rename-url=\"/console/chat/workspace/rename\"") {
		t.Fatalf("chat page is missing workspace rename wiring: %s", html)
	}
	if !strings.Contains(html, "data-chat-workspace-delete-url=\"/console/chat/workspace/path\"") {
		t.Fatalf("chat page is missing workspace delete wiring: %s", html)
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
		"data-chat-editor-code",
		"data-chat-editor-line-numbers",
		"data-chat-editor-highlight",
		"data-chat-editor-input",
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("chat page did not render workbench marker %s: %s", marker, html)
		}
	}
	if !strings.Contains(html, "data-chat-shell-dock") || !strings.Contains(html, "data-chat-shell-tabs") || !strings.Contains(html, "data-chat-shell-action=\"hide\"") {
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

func TestChatEditorFallbackAssets(t *testing.T) {
	cssBytes, err := consoleAssets.ReadFile("static/console.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	for _, marker := range []string{
		".chat-editor-code[data-chat-editor-highlight-ready=\"true\"] .chat-editor-input",
		".chat-editor-code[data-chat-editor-highlight-ready=\"true\"] .chat-editor-input::selection",
		"-webkit-text-fill-color: #d4d4d4;",
		"-webkit-text-fill-color: transparent;",
		"z-index: 2;",
	} {
		if !strings.Contains(css, marker) {
			t.Fatalf("console css is missing editor fallback marker %s", marker)
		}
	}

	jsBytes, err := consoleAssets.ReadFile("static/chat-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBytes)
	for _, marker := range []string{
		"function setWorkspaceEditorHighlightReady(host, ready)",
		"host.dataset.chatEditorHighlightReady = \"true\"",
		"delete host.dataset.chatEditorHighlightReady",
		"setWorkspaceEditorHighlightReady(host, false)",
		"setWorkspaceEditorHighlightReady(host, true)",
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("chat workspace js is missing editor fallback marker %s", marker)
		}
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
	if !strings.Contains(html, "data-chat-shell-socket-url=\"/console/chat/shell/ws?session=session-shell&amp;terminal=default\"") {
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
	if payload.TerminalID != consoleChatShellDefaultID || payload.SocketURL != "/console/chat/shell/ws?session=session-shell&terminal=default" {
		t.Fatalf("unexpected shell socket url: %+v", payload)
	}
}

func TestChatShellStateEndpointPersistsTerminalTabs(t *testing.T) {
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
	requestBody := []byte(`{"sessionID":"session-shell","activeTerminalID":"term-two","hidden":false,"instances":[{"terminalID":"term-one","label":"Terminal 1","sessionID":"stale"},{"terminalID":"term-two","label":"Terminal 2","sessionID":"stale","meta":{"workerID":"old-worker","currentWorkingDirectory":"/srv/aiyolo/workspace/session-shell/app"}}]}`)
	response, err := client.Post(server.URL+"/console/chat/shell/state", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("save shell state status=%d body=%s", response.StatusCode, body)
	}
	var saved consoleChatShellStateResponse
	if err := json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.ShellState.ActiveTerminalID != "term-two" || len(saved.ShellState.Instances) != 2 {
		t.Fatalf("unexpected saved shell state: %+v", saved.ShellState)
	}
	if saved.ShellState.Instances[0].SessionID != "session-shell" || saved.ShellState.Instances[0].Meta.WorkerID != "worker-0" {
		t.Fatalf("shell state should be normalized to the active cloud session: %+v", saved.ShellState.Instances[0])
	}
	if saved.ShellState.Instances[1].CurrentWorkingDirectory != "/srv/aiyolo/workspace/session-shell/app" || saved.ShellState.Instances[1].Meta.CurrentWorkingDirectory != "/srv/aiyolo/workspace/session-shell/app" {
		t.Fatalf("shell state should preserve the terminal cwd: %+v", saved.ShellState.Instances[1])
	}

	stored, err := store.GetCloudAgentSession(ctx, "admin@example.com", consoleChatCloudAgentSessionID("session-shell"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.ShellStateJSON, `"terminalID":"term-two"`) || !strings.Contains(stored.ShellStateJSON, `"socketURL":"/console/chat/shell/ws?session=session-shell`) || !strings.Contains(stored.ShellStateJSON, `"currentWorkingDirectory":"/srv/aiyolo/workspace/session-shell/app"`) {
		t.Fatalf("shell state was not persisted with normalized terminal metadata: %s", stored.ShellStateJSON)
	}

	getResponse, err := client.Get(server.URL + "/console/chat/shell/state?session=session-shell")
	if err != nil {
		t.Fatal(err)
	}
	defer getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResponse.Body)
		t.Fatalf("get shell state status=%d body=%s", getResponse.StatusCode, body)
	}
	var loaded consoleChatShellStateResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.ShellState.ActiveTerminalID != "term-two" || len(loaded.ShellState.Instances) != 2 {
		t.Fatalf("unexpected loaded shell state: %+v", loaded.ShellState)
	}
	if loaded.ShellState.Instances[1].SocketURL != "/console/chat/shell/ws?session=session-shell&terminal=term-two" {
		t.Fatalf("unexpected terminal socket URL: %+v", loaded.ShellState.Instances[1])
	}

	clearResponse, err := client.Post(server.URL+"/console/chat/shell/state", "application/json", strings.NewReader(`{"sessionID":"session-shell","instances":[],"hidden":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer clearResponse.Body.Close()
	if clearResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(clearResponse.Body)
		t.Fatalf("clear shell state status=%d body=%s", clearResponse.StatusCode, body)
	}
	stored, err = store.GetCloudAgentSession(ctx, "admin@example.com", consoleChatCloudAgentSessionID("session-shell"))
	if err != nil {
		t.Fatal(err)
	}
	if stored.ShellStateJSON != "" {
		t.Fatalf("closed shell state should be cleared, got %s", stored.ShellStateJSON)
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

func TestChatWorkspaceRuntimeUnavailableDetectsMissingASSEndpoint(t *testing.T) {
	err := errors.New("aiyolo-ass endpoint not available: PUT /v1/fs/directory returned HTTP 404: 404 page not found")
	if !consoleChatWorkspaceRuntimeUnavailable(err) {
		t.Fatalf("missing ASS endpoint should trigger workspace runtime restore: %v", err)
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

func TestCreateChatWorkspaceFileAndDirectory(t *testing.T) {
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
		t.Fatalf("workspace create should use the active session fast path without ensuring runtime worker=%+v key=%+v proxy=%+v options=%+v", worker, key, proxy, options)
		return workerops.CloudAgentInstance{}, nil
	}
	handler.createCloudAgentWorkspaceFile = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string, content string, mkdirP bool) (workerops.CloudAgentWorkspaceFile, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace create file inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if relativePath != "src/new.go" || content != "package main\n" || !mkdirP {
			t.Fatalf("unexpected workspace create file payload path=%q content=%q mkdirP=%v", relativePath, content, mkdirP)
		}
		return workerops.CloudAgentWorkspaceFile{Path: relativePath, Bytes: int64(len(content))}, nil
	}
	handler.createCloudAgentWorkspaceDir = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string, mkdirP bool) (workerops.CloudAgentWorkspaceDirectory, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace create directory inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if relativePath != "src/assets" || !mkdirP {
			t.Fatalf("unexpected workspace create directory payload path=%q mkdirP=%v", relativePath, mkdirP)
		}
		return workerops.CloudAgentWorkspaceDirectory{Path: relativePath}, nil
	}
	handler.uploadCloudAgentWorkspaceFile = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string, content []byte, mkdirP bool, overwrite bool) (workerops.CloudAgentWorkspaceFile, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace upload inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if relativePath != "src/assets/blob.bin" || string(content) != string([]byte{0, 1, 2, 3}) || !mkdirP || !overwrite {
			t.Fatalf("unexpected workspace upload payload path=%q content=%v mkdirP=%v overwrite=%v", relativePath, content, mkdirP, overwrite)
		}
		return workerops.CloudAgentWorkspaceFile{Path: relativePath, Bytes: int64(len(content))}, nil
	}
	handler.renameCloudAgentWorkspacePath = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, oldPath string, newPath string) (workerops.CloudAgentWorkspaceRename, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace rename inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if oldPath != "src/assets" || newPath != "src/static" {
			t.Fatalf("unexpected workspace rename payload old=%q new=%q", oldPath, newPath)
		}
		return workerops.CloudAgentWorkspaceRename{OldPath: oldPath, Path: newPath}, nil
	}
	handler.downloadCloudAgentWorkspaceFile = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string) (workerops.CloudAgentWorkspaceDownload, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace download inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if relativePath != "src/static/data.bin" {
			t.Fatalf("unexpected workspace download path=%q", relativePath)
		}
		return workerops.CloudAgentWorkspaceDownload{Path: relativePath, Name: "data.bin", Size: 3, MediaType: "application/octet-stream", Content: []byte{9, 8, 7}}, nil
	}
	handler.copyCloudAgentWorkspacePath = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, oldPath string, newPath string) (workerops.CloudAgentWorkspaceCopy, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace copy inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if oldPath != "src/static" || newPath != "src/static-copy" {
			t.Fatalf("unexpected workspace copy payload old=%q new=%q", oldPath, newPath)
		}
		return workerops.CloudAgentWorkspaceCopy{SourcePath: oldPath, Path: newPath}, nil
	}
	handler.deleteCloudAgentWorkspacePath = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, relativePath string) (workerops.CloudAgentWorkspaceDelete, error) {
		if worker.ID != "worker-0" || key.ID != "ssh-key-1" || account.ContainerName != "aiyolo-cloud-agent-worker-0" || session.ChatSessionID != "session-shell" {
			t.Fatalf("unexpected workspace delete inputs worker=%+v key=%+v account=%+v session=%+v", worker, key, account, session)
		}
		if relativePath != "src/static-copy" {
			t.Fatalf("unexpected workspace delete path=%q", relativePath)
		}
		return workerops.CloudAgentWorkspaceDelete{Path: relativePath}, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	fileResponse, err := client.Post(server.URL+"/console/chat/workspace/file?session=session-shell", "application/json", strings.NewReader(`{"path":"src/new.go","content":"package main\n","create":true,"mkdir_p":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer fileResponse.Body.Close()
	if fileResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(fileResponse.Body)
		t.Fatalf("chat workspace create file status=%d body=%s", fileResponse.StatusCode, body)
	}
	var filePayload consoleChatWorkspaceFileResponse
	if err := json.NewDecoder(fileResponse.Body).Decode(&filePayload); err != nil {
		t.Fatal(err)
	}
	if filePayload.Status != "created" || filePayload.Path != "src/new.go" || filePayload.Bytes != 13 || filePayload.Notice != "文件已创建。" {
		t.Fatalf("unexpected workspace create file payload: %+v", filePayload)
	}

	directoryResponse, err := client.Post(server.URL+"/console/chat/workspace/directory?session=session-shell", "application/json", strings.NewReader(`{"path":"src/assets","mkdir_p":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer directoryResponse.Body.Close()
	if directoryResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(directoryResponse.Body)
		t.Fatalf("chat workspace create directory status=%d body=%s", directoryResponse.StatusCode, body)
	}
	var directoryPayload consoleChatWorkspaceDirectoryResponse
	if err := json.NewDecoder(directoryResponse.Body).Decode(&directoryPayload); err != nil {
		t.Fatal(err)
	}
	if directoryPayload.Status != "created" || directoryPayload.Path != "src/assets" || directoryPayload.Notice != "目录已创建。" {
		t.Fatalf("unexpected workspace create directory payload: %+v", directoryPayload)
	}

	var uploadBody bytes.Buffer
	uploadWriter := multipart.NewWriter(&uploadBody)
	if err := uploadWriter.WriteField("path", "src/assets/blob.bin"); err != nil {
		t.Fatal(err)
	}
	if err := uploadWriter.WriteField("overwrite", "true"); err != nil {
		t.Fatal(err)
	}
	uploadPart, err := uploadWriter.CreateFormFile("file", "blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uploadPart.Write([]byte{0, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := uploadWriter.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/workspace/upload?session=session-shell", &uploadBody)
	if err != nil {
		t.Fatal(err)
	}
	uploadRequest.Header.Set("Content-Type", uploadWriter.FormDataContentType())
	uploadResponse, err := client.Do(uploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer uploadResponse.Body.Close()
	if uploadResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(uploadResponse.Body)
		t.Fatalf("chat workspace upload status=%d body=%s", uploadResponse.StatusCode, body)
	}
	var uploadPayload consoleChatWorkspaceFileResponse
	if err := json.NewDecoder(uploadResponse.Body).Decode(&uploadPayload); err != nil {
		t.Fatal(err)
	}
	if uploadPayload.Status != "uploaded" || uploadPayload.Path != "src/assets/blob.bin" || uploadPayload.Bytes != 4 || uploadPayload.Notice != "文件已上传。" {
		t.Fatalf("unexpected workspace upload payload: %+v", uploadPayload)
	}
	renameResponse, err := client.Post(server.URL+"/console/chat/workspace/rename?session=session-shell", "application/json", strings.NewReader(`{"path":"src/assets","new_path":"src/static"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer renameResponse.Body.Close()
	if renameResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(renameResponse.Body)
		t.Fatalf("chat workspace rename status=%d body=%s", renameResponse.StatusCode, body)
	}
	var renamePayload consoleChatWorkspaceRenameResponse
	if err := json.NewDecoder(renameResponse.Body).Decode(&renamePayload); err != nil {
		t.Fatal(err)
	}
	if renamePayload.Status != "renamed" || renamePayload.OldPath != "src/assets" || renamePayload.Path != "src/static" || renamePayload.Notice != "已重命名。" {
		t.Fatalf("unexpected workspace rename payload: %+v", renamePayload)
	}
	downloadResponse, err := client.Get(server.URL + "/console/chat/workspace/download?session=session-shell&path=src/static/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer downloadResponse.Body.Close()
	if downloadResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(downloadResponse.Body)
		t.Fatalf("chat workspace download status=%d body=%s", downloadResponse.StatusCode, body)
	}
	if contentType := downloadResponse.Header.Get("Content-Type"); contentType != "application/octet-stream" {
		t.Fatalf("unexpected download content type: %q", contentType)
	}
	if disposition := downloadResponse.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "data.bin") {
		t.Fatalf("unexpected download disposition: %q", disposition)
	}
	if content, err := io.ReadAll(downloadResponse.Body); err != nil || string(content) != string([]byte{9, 8, 7}) {
		t.Fatalf("unexpected download content=%v err=%v", content, err)
	}

	copyResponse, err := client.Post(server.URL+"/console/chat/workspace/copy?session=session-shell", "application/json", strings.NewReader(`{"path":"src/static","new_path":"src/static-copy"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer copyResponse.Body.Close()
	if copyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(copyResponse.Body)
		t.Fatalf("chat workspace copy status=%d body=%s", copyResponse.StatusCode, body)
	}
	var copyPayload consoleChatWorkspaceCopyResponse
	if err := json.NewDecoder(copyResponse.Body).Decode(&copyPayload); err != nil {
		t.Fatal(err)
	}
	if copyPayload.Status != "copied" || copyPayload.SourcePath != "src/static" || copyPayload.Path != "src/static-copy" || copyPayload.Notice != "已复制。" {
		t.Fatalf("unexpected workspace copy payload: %+v", copyPayload)
	}

	deleteRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/console/chat/workspace/path?session=session-shell", strings.NewReader(`{"path":"src/static-copy"}`))
	if err != nil {
		t.Fatal(err)
	}
	deleteRequest.Header.Set("Content-Type", "application/json")
	deleteResponse, err := client.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(deleteResponse.Body)
		t.Fatalf("chat workspace delete status=%d body=%s", deleteResponse.StatusCode, body)
	}
	var deletePayload consoleChatWorkspaceDeleteResponse
	if err := json.NewDecoder(deleteResponse.Body).Decode(&deletePayload); err != nil {
		t.Fatal(err)
	}
	if deletePayload.Status != "deleted" || deletePayload.Path != "src/static-copy" || deletePayload.Notice != "已永久删除。" {
		t.Fatalf("unexpected workspace delete payload: %+v", deletePayload)
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
	var openContextHadDeadline atomic.Bool
	var openContextHadDone atomic.Bool
	handler.openCloudAgentShell = func(openCtx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, cols, rows int) (workerops.InteractiveShell, error) {
		if _, ok := openCtx.Deadline(); ok {
			openContextHadDeadline.Store(true)
		}
		if openCtx.Done() != nil {
			openContextHadDone.Store(true)
		}
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
	if openContextHadDeadline.Load() || openContextHadDone.Load() {
		t.Fatal("interactive shell open context must not expire and close the terminal after connection")
	}
}

func TestChatShellSocketReusesTerminalAfterWebSocketDisconnect(t *testing.T) {
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
		return workerops.CloudAgentInstance{
			Status:        domain.CloudAgentStatusRunning,
			WorkerID:      worker.ID,
			ContainerID:   "container-shell",
			ContainerName: "aiyolo-cloud-agent-worker-0",
			WorkspacePath: options.WorkspacePath,
		}, nil
	}
	var openCalls atomic.Int32
	handler.openCloudAgentShell = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, session domain.CloudAgentSession, cols, rows int) (workerops.InteractiveShell, error) {
		openCalls.Add(1)
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
	dialShell := func() *websocket.Conn {
		t.Helper()
		config, err := websocket.NewConfig("ws"+strings.TrimPrefix(server.URL, "http")+"/console/chat/shell/ws?session=session-shell&terminal=term-stable", server.URL)
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
		if err := ws.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return ws
	}

	ws1 := dialShell()
	if err := websocket.JSON.Send(ws1, consoleChatShellSocketRequest{Type: "input", Data: "echo first\r"}); err != nil {
		t.Fatal(err)
	}
	if write := fakeShell.waitWrite(t); write != "echo first\r" {
		t.Fatalf("unexpected first shell input: %q", write)
	}
	if err := ws1.Close(); err != nil {
		t.Fatal(err)
	}

	ws2 := dialShell()
	defer ws2.Close()
	if err := websocket.JSON.Send(ws2, consoleChatShellSocketRequest{Type: "input", Data: "echo second\r"}); err != nil {
		t.Fatal(err)
	}
	if write := fakeShell.waitWrite(t); write != "echo second\r" {
		t.Fatalf("unexpected second shell input: %q", write)
	}
	if openCalls.Load() != 1 {
		t.Fatalf("open shell calls=%d, want 1", openCalls.Load())
	}
	if ensureCalls.Load() != 2 {
		t.Fatalf("ensure calls=%d, want 2", ensureCalls.Load())
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
