package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/app"
	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

const testAPIKey = "aiyolo_test_abcdefghijklmnopqrstuvwxyz"

func TestGatewayInterfaces(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			assertHeader(t, r, "Authorization", "Bearer upstream-openai")
			assertModel(t, r, "upstream-chat")
			writeJSON(t, w, map[string]any{"id": "chatcmpl_test", "object": "chat.completion", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10}})
		case "/v1/responses":
			assertHeader(t, r, "Authorization", "Bearer upstream-openai")
			assertModel(t, r, "upstream-chat")
			writeJSON(t, w, map[string]any{"id": "resp_test", "object": "response", "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 6, "total_tokens": 11}})
		case "/v1/completions":
			assertModel(t, r, "upstream-chat")
			writeJSON(t, w, map[string]any{"id": "cmpl_test", "choices": []any{map[string]any{"text": "middle"}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 2, "total_tokens": 4}})
		case "/v1/embeddings":
			assertModel(t, r, "upstream-chat")
			writeJSON(t, w, map[string]any{"object": "list", "data": []any{}, "usage": map[string]any{"prompt_tokens": 4, "total_tokens": 4}})
		case "/v1/messages":
			assertHeader(t, r, "x-api-key", "upstream-anthropic")
			assertHeader(t, r, "anthropic-version", "2023-06-01")
			assertHeader(t, r, "anthropic-beta", "prompt-caching-2024-07-31")
			assertModel(t, r, "upstream-claude")
			writeJSON(t, w, map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "usage": map[string]any{"input_tokens": 9, "output_tokens": 4, "cache_creation_input_tokens": 1, "cache_read_input_tokens": 2}})
		case "/v1/messages/count_tokens":
			assertHeader(t, r, "x-api-key", "upstream-anthropic")
			assertModel(t, r, "upstream-claude")
			writeJSON(t, w, map[string]any{"input_tokens": 13})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	t.Run("models", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/models", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
		body, _ := io.ReadAll(response.Body)
		if !bytes.Contains(body, []byte("public-chat")) || !bytes.Contains(body, []byte("claude-public")) {
			t.Fatalf("models response missing routes: %s", body)
		}
	})

	t.Run("openai chat completions", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
		usage := lastUsage(t, store)
		if usage.InputTokens != 7 || usage.OutputTokens != 3 || usage.TotalTokens != 10 || usage.ProviderID != "openai-test" {
			t.Fatalf("unexpected usage: %+v", usage)
		}
	})

	t.Run("openai responses", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/responses", http.MethodPost, []byte(`{"model":"public-chat","input":"hi"}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
		usage := lastUsage(t, store)
		if usage.Endpoint != "/v1/responses" || usage.TotalTokens != 11 {
			t.Fatalf("unexpected responses usage: %+v", usage)
		}
	})

	t.Run("fim completions", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/completions", http.MethodPost, []byte(`{"model":"public-chat","prompt":"a","suffix":"c","max_tokens":8}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
		if usage := lastUsage(t, store); usage.Endpoint != "/v1/completions" || usage.TotalTokens != 4 {
			t.Fatalf("unexpected completions usage: %+v", usage)
		}
	})

	t.Run("embeddings", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/embeddings", http.MethodPost, []byte(`{"model":"public-chat","input":"hello"}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
		if usage := lastUsage(t, store); usage.Endpoint != "/v1/embeddings" || usage.InputTokens != 4 {
			t.Fatalf("unexpected embeddings usage: %+v", usage)
		}
	})

	t.Run("anthropic messages", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/messages", http.MethodPost, []byte(`{"model":"claude-public","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"x-api-key": testAPIKey, "anthropic-version": "2023-06-01", "anthropic-beta": "prompt-caching-2024-07-31"})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
		usage := lastUsage(t, store)
		if usage.InputTokens != 9 || usage.OutputTokens != 4 || usage.CacheCreationTokens != 1 || usage.CacheReadTokens != 2 || usage.TotalTokens != 16 {
			t.Fatalf("unexpected anthropic usage: %+v", usage)
		}
	})

	t.Run("anthropic count tokens", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/messages/count_tokens", http.MethodPost, []byte(`{"model":"claude-public","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey, "anthropic-version": "2023-06-01"})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
	})

	t.Run("invalid auth", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat"}`), nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d", response.StatusCode)
		}
	})
}

func TestStreamingFlushAndUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "data: [DONE]") {
		t.Fatalf("missing done event: %s", body)
	}
	usage := lastUsage(t, store)
	if !usage.Stream || usage.TotalTokens != 5 {
		t.Fatalf("unexpected streaming usage: %+v", usage)
	}
}

func TestQuotaRPMEnforced(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"id": "chatcmpl_test", "object": "chat.completion", "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	ctx := context.Background()
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "test-key", Name: "limited", KeyHash: auth.HashAPIKey(testAPIKey), Prefix: auth.Prefix(testAPIKey), UserID: "user-1", OrganizationID: "org-1", ProjectID: "proj-1", Status: domain.StatusActive, RPMLimit: 1, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	first := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status=%d", first.StatusCode)
	}
	second := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("second status=%d body=%s", second.StatusCode, body)
	}
}

func testConfig() app.Config {
	return app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
}

func testStore(t *testing.T, upstreamURL string) *storage.MemoryStore {
	t.Helper()
	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "test-key", Name: "test", KeyHash: auth.HashAPIKey(testAPIKey), Prefix: auth.Prefix(testAPIKey), UserID: "user-1", OrganizationID: "org-1", ProjectID: "proj-1", Status: domain.StatusActive, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openai-test", Name: "OpenAI Test", BaseURL: upstreamURL, Protocol: domain.ProtocolOpenAI, MasterKey: "upstream-openai", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "anthropic-test", Name: "Anthropic Test", BaseURL: upstreamURL, Protocol: domain.ProtocolAnthropic, MasterKey: "upstream-anthropic", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "public-chat", ProviderID: "openai-test", UpstreamModel: "upstream-chat", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "claude-public", ProviderID: "anthropic-test", UpstreamModel: "upstream-claude", Protocol: domain.ProtocolAnthropic, ProxyProfileID: "direct", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return store
}

func doRequest(t *testing.T, url string, method string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertHeader(t *testing.T, r *http.Request, name, expected string) {
	t.Helper()
	if actual := r.Header.Get(name); actual != expected {
		t.Fatalf("%s=%q, want %q", name, actual, expected)
	}
}

func assertModel(t *testing.T, r *http.Request, expected string) {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != expected {
		t.Fatalf("model=%v, want %s", payload["model"], expected)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}

func lastUsage(t *testing.T, store *storage.MemoryStore) domain.UsageRecord {
	t.Helper()
	items, err := store.ListUsage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("usage ledger is empty")
	}
	return items[0]
}
