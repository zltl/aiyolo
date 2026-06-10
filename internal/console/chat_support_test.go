package console

import (
	"context"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestFindConsoleChatRouteMatchesGPTImage2Aliases(t *testing.T) {
	routes := []consoleChatRouteView{
		{PublicName: "chatgpt-image-2", UpstreamModel: "chatgpt-image-2"},
	}

	for _, requested := range []string{"gpt-image-2", "openai/gpt-image-2", "chatgpt-image-2"} {
		route, ok := findConsoleChatRoute(routes, requested)
		if !ok {
			t.Fatalf("expected alias %q to resolve", requested)
		}
		if route.PublicName != "chatgpt-image-2" {
			t.Fatalf("alias %q resolved to unexpected route: %+v", requested, route)
		}
	}
}

func TestFindConsoleChatRouteMatchesFluxAliases(t *testing.T) {
	routes := []consoleChatRouteView{
		{PublicName: "flux-1.1-pro-ultra", UpstreamModel: "black-forest-labs/flux-1.1-pro-ultra"},
	}

	for _, requested := range []string{"flux-1.1-pro-ultra", "black-forest-labs/flux-1.1-pro-ultra", "flux"} {
		route, ok := findConsoleChatRoute(routes, requested)
		if !ok {
			t.Fatalf("expected alias %q to resolve", requested)
		}
		if route.PublicName != "flux-1.1-pro-ultra" {
			t.Fatalf("alias %q resolved to unexpected route: %+v", requested, route)
		}
	}
}

func TestConsoleChatCloudAgentAllowedModelsReusesExistingCloudAgentKeyScope(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	if _, userAPIKey, err := newConsoleAPIKey(apiKeySpec{
		ID:            "chat-scope-key",
		Name:          "Chat Scope Key",
		Kind:          "live",
		UserID:        "admin@example.com",
		AllowedModels: []string{"gpt-5.4"},
	}); err != nil {
		t.Fatal(err)
	} else if err := store.CreateAPIKey(ctx, userAPIKey); err != nil {
		t.Fatal(err)
	}
	clearKey, apiKey, err := newConsoleAPIKey(apiKeySpec{
		ID:               "cloud-agent-worker-0-key",
		Name:             "Cloud Agent worker-0",
		Kind:             "live",
		UserID:           "admin@example.com",
		AllowedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic},
		AllowedModels:    []string{"gpt-5.4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}
	seedChatEnvironmentWorker(t, ctx, store, "worker-0")
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{
		ID:              consoleChatCloudAgentAccountID("worker-0"),
		UserID:          "admin@example.com",
		WorkerID:        "worker-0",
		AgentType:       domain.CloudAgentTypeClaudeCode,
		ModelPublicName: "gpt-5.4",
		Credential:      clearKey,
		Status:          domain.CloudAgentStatusRunning,
		CreatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	if _, err := store.FindAPIKeyByHash(ctx, auth.HashAPIKey(clearKey)); err != nil {
		t.Fatalf("seed key missing before resolve: %v", err)
	}
	if _, err := handler.resolveConsoleChatAllowedModels(ctx, "admin@example.com", consoleChatEnvironmentLocal); err != nil {
		t.Fatal(err)
	} else if _, err := store.FindAPIKeyByHash(ctx, auth.HashAPIKey(clearKey)); err != nil {
		t.Fatalf("seed key missing after resolve: %v", err)
	}
	allowedModels, err := handler.consoleChatCloudAgentAllowedModels(ctx, "admin@example.com", &consoleChatPageState{
		Form: consoleChatFormView{PublicName: "anthropic/claude-opus-4.7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"gpt-5.4", "openai/gpt-image-2", "gpt-image-2", "chatgpt-image-2", "black-forest-labs/flux-1.1-pro-ultra", "flux-1.1-pro-ultra", "black-forest-labs/flux.2-pro", "black-forest-labs/flux.2-flex"}
	if !consoleChatSameStringSet(allowedModels, expected) {
		t.Fatalf("unexpected cloud agent allowed models: %+v", allowedModels)
	}
	stored, err := store.FindAPIKeyByHash(ctx, auth.HashAPIKey(clearKey))
	if err != nil {
		t.Fatal(err)
	}
	if !consoleChatSameStringSet(consoleChatExpandAllowedModels(stored.AllowedModels), allowedModels) {
		t.Fatalf("stored expanded models=%+v allowed models=%+v", stored.AllowedModels, allowedModels)
	}
	account, err := handler.ensureConsoleChatEnvironmentAPIKey(ctx, "admin@example.com", "worker-0", domain.CloudAgentAccount{
		ID:         consoleChatCloudAgentAccountID("worker-0"),
		Credential: clearKey,
	}, allowedModels, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if account.Credential != clearKey {
		t.Fatalf("expected credential to stay unchanged, got %q", account.Credential)
	}
}

func TestConsoleChatAllowedModelsFromUserAPIKeysUsesSeedAllowedModels(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateAPIKey(ctx, domain.APIKey{
		ID:            "seed",
		Name:          "Seed API Key",
		KeyHash:       "seed-hash",
		Prefix:        "aiyolo_live_seed",
		UserID:        "local",
		Status:        domain.StatusActive,
		AllowedModels: []string{"deepseek-v4-pro", "gpt-5.4"},
		CreatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	resolution, err := handler.consoleChatAllowedModelsFromUserAPIKeys(ctx, "i@quant67.com")
	if err != nil {
		t.Fatal(err)
	}
	allowed := consoleChatStringSet(resolution.Models)
	for _, model := range []string{"deepseek-v4-pro", "gpt-5.4"} {
		if _, ok := allowed[model]; !ok {
			t.Fatalf("expected seed allowed models to include %q, got %+v", model, resolution.Models)
		}
	}
}

func TestConsoleChatAllowedModelsFromUserAPIKeysHonorsUnrestrictedLiveKey(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateAPIKey(ctx, domain.APIKey{
		ID:        "seed",
		Name:      "Seed API Key",
		KeyHash:   "seed-hash",
		Prefix:    "aiyolo_live_seed",
		UserID:    "local",
		Status:    domain.StatusActive,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{
		ID:        "live-open",
		Name:      "Open Live Key",
		KeyHash:   "live-open-hash",
		Prefix:    "aiyolo_live_open",
		UserID:    "local",
		Status:    domain.StatusActive,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	resolution, err := handler.consoleChatAllowedModelsFromUserAPIKeys(ctx, "i@quant67.com")
	if err != nil {
		t.Fatal(err)
	}
	if !resolution.Unrestricted {
		t.Fatalf("expected unrestricted live key to unlock chat routes, got %+v", resolution)
	}
}

func TestConsoleChatAllowedModelsFromUserAPIKeysIgnoresSeedKeyWhenNoLiveKeyScopes(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateAPIKey(ctx, domain.APIKey{
		ID:        "seed",
		Name:      "Seed API Key",
		KeyHash:   "seed-hash",
		Prefix:    "aiyolo_live_seed",
		UserID:    "local",
		Status:    domain.StatusActive,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	resolution, err := handler.consoleChatAllowedModelsFromUserAPIKeys(ctx, "i@quant67.com")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Unrestricted || len(resolution.Models) != 0 {
		t.Fatalf("expected no chat models from seed key only, got %+v", resolution)
	}
}

func TestConsoleChatAllowedModelsFromUserAPIKeysPrefersExplicitLiveKeyOverSeedKey(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateAPIKey(ctx, domain.APIKey{
		ID:        "seed",
		Name:      "Seed API Key",
		KeyHash:   "seed-hash",
		Prefix:    "aiyolo_live_seed",
		UserID:    "local",
		Status:    domain.StatusActive,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{
		ID:            "live-scope",
		Name:          "My Live Key",
		KeyHash:       "live-hash",
		Prefix:        "aiyolo_live_scope",
		UserID:        "local",
		Status:        domain.StatusActive,
		AllowedModels: []string{"gpt-5.4", "deepseek-v4-pro"},
		CreatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	resolution, err := handler.consoleChatAllowedModelsFromUserAPIKeys(ctx, "i@quant67.com")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Unrestricted {
		t.Fatalf("expected scoped models, got unrestricted resolution: %+v", resolution)
	}
	allowed := consoleChatStringSet(resolution.Models)
	for _, model := range []string{"gpt-5.4", "deepseek-v4-pro"} {
		if _, ok := allowed[model]; !ok {
			t.Fatalf("expected scoped models to include %q, got %+v", model, resolution.Models)
		}
	}
}

func TestConsoleChatFilterRoutesByAllowedModelsReturnsEmptyWithoutAllowedModels(t *testing.T) {
	routes := []consoleChatRouteView{{PublicName: "gpt-5.4", UpstreamModel: "openai/gpt-5.4"}}
	if filtered := consoleChatFilterRoutesByAllowedModels(routes, nil); len(filtered) != 0 {
		t.Fatalf("expected no routes without allowed models, got %+v", filtered)
	}
}

func TestConsoleChatFilterRoutesByAllowedModels(t *testing.T) {
	routes := []consoleChatRouteView{
		{PublicName: "deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro"},
		{PublicName: "openai/gpt-5.4", UpstreamModel: "openai/gpt-5.4"},
		{PublicName: "chatgpt-image-2", UpstreamModel: "chatgpt-image-2"},
		{PublicName: "anthropic/claude-opus-4.7", UpstreamModel: "anthropic/claude-opus-4.7"},
	}
	filtered := consoleChatFilterRoutesByAllowedModels(routes, []string{"deepseek-v4-pro", "openai/gpt-5.4", "gpt-image-2"})
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered routes, got %d: %+v", len(filtered), filtered)
	}
	if filtered[0].PublicName != "deepseek-v4-pro" || filtered[1].PublicName != "openai/gpt-5.4" || filtered[2].PublicName != "chatgpt-image-2" {
		t.Fatalf("unexpected filtered routes: %+v", filtered)
	}
}

func TestConsoleChatFilterRoutesByAllowedModelsMatchesShortPublicNames(t *testing.T) {
	routes := []consoleChatRouteView{
		{PublicName: "gpt-5.4", UpstreamModel: "openai/gpt-5.4"},
		{PublicName: "google/gemini-3.1-pro-preview", UpstreamModel: "google/gemini-3.1-pro-preview"},
	}
	filtered := consoleChatFilterRoutesByAllowedModels(routes, []string{"gpt-5.4"})
	if len(filtered) != 1 || filtered[0].PublicName != "gpt-5.4" {
		t.Fatalf("expected short alias to match routed model, got %+v", filtered)
	}
}
