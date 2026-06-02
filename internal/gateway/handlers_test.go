package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
		var payload struct {
			Object string `json:"object"`
			Data   []struct {
				ID                  string   `json:"id"`
				Object              string   `json:"object"`
				ContextLength       int      `json:"context_length"`
				SupportedParameters []string `json:"supported_parameters"`
				Pricing             struct {
					Prompt          string `json:"prompt"`
					Completion      string `json:"completion"`
					InputCacheRead  string `json:"input_cache_read"`
					InputCacheWrite string `json:"input_cache_write"`
				} `json:"pricing"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Object != "list" || len(payload.Data) < 2 {
			t.Fatalf("unexpected models payload: %+v", payload)
		}
		foundPublicChat := false
		foundClaude := false
		for _, model := range payload.Data {
			switch model.ID {
			case "public-chat":
				foundPublicChat = true
				if model.ContextLength != 128000 || model.Pricing.Prompt != "0.0000025" || model.Pricing.Completion != "0.00001" {
					t.Fatalf("unexpected public-chat model payload: %+v", model)
				}
				if !containsString(model.SupportedParameters, "tools") || !containsString(model.SupportedParameters, "response_format") {
					t.Fatalf("supported_parameters missing openai fields: %+v", model.SupportedParameters)
				}
			case "claude-public":
				foundClaude = true
				if !containsString(model.SupportedParameters, "system") || !containsString(model.SupportedParameters, "top_k") {
					t.Fatalf("supported_parameters missing anthropic fields: %+v", model.SupportedParameters)
				}
			}
		}
		if !foundPublicChat || !foundClaude {
			t.Fatalf("models response missing routes: %+v", payload.Data)
		}
	})

	t.Run("openrouter aliases", func(t *testing.T) {
		models := doRequest(t, server.URL+"/api/v1/models", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer models.Body.Close()
		if models.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(models.Body)
			t.Fatalf("models status=%d body=%s", models.StatusCode, body)
		}

		chat := doRequest(t, server.URL+"/api/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer chat.Body.Close()
		if chat.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(chat.Body)
			t.Fatalf("chat status=%d body=%s", chat.StatusCode, body)
		}

		messages := doRequest(t, server.URL+"/api/v1/messages", http.MethodPost, []byte(`{"model":"claude-public","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"x-api-key": testAPIKey, "anthropic-version": "2023-06-01", "anthropic-beta": "prompt-caching-2024-07-31"})
		defer messages.Body.Close()
		if messages.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(messages.Body)
			t.Fatalf("messages status=%d body=%s", messages.StatusCode, body)
		}
	})

	t.Run("openai chat completions", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
		usage := lastUsage(t, store)
		if usage.InputTokens != 7 || usage.OutputTokens != 3 || usage.TotalTokens != 10 || usage.ProviderID != "openai-test" || usage.CostMicroCents != 4750 {
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

func TestStreamingAllowsActiveLongRunningStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(700 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(700 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openai-test", Name: "OpenAI Test", BaseURL: upstream.URL, Protocol: domain.ProtocolOpenAI, MasterKey: "upstream-openai", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	payload := string(body)
	if !strings.Contains(payload, `"content":"he"`) || !strings.Contains(payload, `"content":"llo"`) || !strings.Contains(payload, "data: [DONE]") {
		t.Fatalf("unexpected streaming payload: %s", payload)
	}
	usage := lastUsage(t, store)
	if !usage.Stream || usage.TotalTokens != 5 {
		t.Fatalf("unexpected streaming usage: %+v", usage)
	}
}

func TestResponsesFallsBackToChatCompletionsWhenUnsupported(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "responses unsupported"}})
		case "/v1/chat/completions":
			assertHeader(t, r, "Authorization", "Bearer upstream-openai")
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["model"] != "upstream-chat" {
				t.Fatalf("model=%#v", payload["model"])
			}
			messages, _ := payload["messages"].([]any)
			if len(messages) != 2 {
				t.Fatalf("messages=%#v", payload["messages"])
			}
			tools, _ := payload["tools"].([]any)
			if len(tools) != 1 {
				t.Fatalf("tools=%#v", payload["tools"])
			}
			tool, _ := tools[0].(map[string]any)
			function, _ := tool["function"].(map[string]any)
			if function["name"] != "shell_command" {
				t.Fatalf("tools=%#v", payload["tools"])
			}
			writeJSON(t, w, map[string]any{"id": "chatcmpl_fallback", "object": "chat.completion", "created": int64(1760000000), "model": "upstream-chat", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok from chat fallback"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	body := []byte(`{"model":"public-chat","instructions":"You are concise.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"tools":[{"type":"function","name":"shell_command","description":"Run a shell command","parameters":{"type":"object"}}],"tool_choice":"auto"}`)
	response := doRequest(t, server.URL+"/v1/responses", http.MethodPost, body, map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["object"] != "response" || payload["output_text"] != "ok from chat fallback" {
		t.Fatalf("unexpected responses payload: %+v", payload)
	}
	usage := lastUsage(t, store)
	if usage.Endpoint != "/v1/responses" || usage.TotalTokens != 5 {
		t.Fatalf("unexpected fallback usage: %+v", usage)
	}
}

func TestStreamingResponsesFallbackWrapsChatCompletionEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			w.WriteHeader(http.StatusNotFound)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"shell_command\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}}]}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":3,\"total_tokens\":7}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	response := doRequest(t, server.URL+"/v1/responses", http.MethodPost, []byte(`{"model":"public-chat","stream":true,"input":"hi"}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	payload := string(body)
	for _, expected := range []string{"event: response.output_text.delta", `"delta":"he"`, `"delta":"llo"`, `"type":"function_call"`, `"call_id":"call_1"`, "event: response.completed", "data: [DONE]"} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("missing %q in payload: %s", expected, payload)
		}
	}
	usage := lastUsage(t, store)
	if !usage.Stream || usage.Endpoint != "/v1/responses" || usage.TotalTokens != 7 {
		t.Fatalf("unexpected streaming fallback usage: %+v", usage)
	}
}

func TestResponsesWebsocketGetsUpgradeRequiredForHTTPFallback(t *testing.T) {
	store := testStore(t, "https://upstream.example.com")
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUpgradeRequired {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
}

func TestOpenRouterSupportsOpenAIAndAnthropic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			assertHeader(t, r, "Authorization", "Bearer sk-or-openrouter")
			assertHeader(t, r, "HTTP-Referer", "https://github.com/zltl/aiyolo")
			assertHeader(t, r, "X-Title", "aiyolo")
			assertModel(t, r, "anthropic/claude-sonnet-4")
			writeJSON(t, w, map[string]any{"id": "chatcmpl_or", "object": "chat.completion", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok from openrouter openai"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 6, "completion_tokens": 3, "total_tokens": 9}})
		case "/v1/messages":
			assertHeader(t, r, "Authorization", "Bearer sk-or-openrouter")
			assertHeader(t, r, "HTTP-Referer", "https://github.com/zltl/aiyolo")
			assertHeader(t, r, "X-Title", "aiyolo")
			assertHeader(t, r, "anthropic-version", "2023-06-01")
			assertHeader(t, r, "anthropic-beta", "prompt-caching-2024-07-31")
			if value := r.Header.Get("x-api-key"); value != "" {
				t.Fatalf("x-api-key=%q, want empty", value)
			}
			assertModel(t, r, "anthropic/claude-sonnet-4")
			writeJSON(t, w, map[string]any{"id": "msg_or", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok from openrouter anthropic"}}, "usage": map[string]any{"input_tokens": 8, "output_tokens": 5, "cache_creation_input_tokens": 1, "cache_read_input_tokens": 2}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "test-key", Name: "test", KeyHash: auth.HashAPIKey(testAPIKey), Prefix: auth.Prefix(testAPIKey), UserID: "user-1", OrganizationID: "org-1", ProjectID: "proj-1", Status: domain.StatusActive, AllowedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: upstream.URL + "/v1", Protocol: domain.ProtocolOpenAI, SupportedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}, MasterKey: "sk-or-openrouter", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "claude-openrouter", ProviderID: "openrouter", UpstreamModel: "anthropic/claude-sonnet-4", Protocol: domain.ProtocolOpenAI, AllowedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}, ProxyProfileID: "direct", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	models := doRequest(t, server.URL+"/v1/models", http.MethodGet, nil, map[string]string{"x-api-key": testAPIKey})
	defer models.Body.Close()
	if models.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(models.Body)
		t.Fatalf("models status=%d body=%s", models.StatusCode, body)
	}

	chat := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"claude-openrouter","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer chat.Body.Close()
	if chat.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(chat.Body)
		t.Fatalf("openai status=%d body=%s", chat.StatusCode, body)
	}
	if usage := lastUsage(t, store); usage.Protocol != domain.ProtocolOpenAI || usage.TotalTokens != 9 || usage.ProviderID != "openrouter" {
		t.Fatalf("unexpected openai usage: %+v", usage)
	}

	messages := doRequest(t, server.URL+"/v1/messages", http.MethodPost, []byte(`{"model":"claude-openrouter","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"x-api-key": testAPIKey, "anthropic-version": "2023-06-01", "anthropic-beta": "prompt-caching-2024-07-31"})
	defer messages.Body.Close()
	if messages.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(messages.Body)
		t.Fatalf("anthropic status=%d body=%s", messages.StatusCode, body)
	}
	if usage := lastUsage(t, store); usage.Protocol != domain.ProtocolAnthropic || usage.TotalTokens != 16 || usage.CacheCreationTokens != 1 || usage.CacheReadTokens != 2 {
		t.Fatalf("unexpected anthropic usage: %+v", usage)
	}
}

func TestGatewayFallbackModelsRetriesRetryableUpstream(t *testing.T) {
	var primaryCalls int32
	var fallbackCalls int32

	primaryUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryCalls, 1)
		assertModel(t, r, "upstream-primary")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"primary unavailable"}}`))
	}))
	defer primaryUpstream.Close()

	fallbackUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		assertModel(t, r, "upstream-fallback")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_fallback", "object": "chat.completion", "model": "upstream-fallback", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok from fallback"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 6, "completion_tokens": 2, "total_tokens": 8}})
	}))
	defer fallbackUpstream.Close()

	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "test-key", Name: "test", KeyHash: auth.HashAPIKey(testAPIKey), Prefix: auth.Prefix(testAPIKey), UserID: "user-1", OrganizationID: "org-1", ProjectID: "proj-1", Status: domain.StatusActive, AllowedProtocols: []string{domain.ProtocolOpenAI}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "provider-primary", Name: "Primary", BaseURL: primaryUpstream.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-primary", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "provider-fallback", Name: "Fallback", BaseURL: fallbackUpstream.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-fallback", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_primary-chat", ModelAlias: "primary-chat", ProviderID: "provider-primary", Currency: "USD", InputPricePerMillionTokens: 100000000, OutputPricePerMillionTokens: 200000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_fallback-chat", ModelAlias: "fallback-chat", ProviderID: "provider-fallback", Currency: "USD", InputPricePerMillionTokens: 80000000, OutputPricePerMillionTokens: 150000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "primary-chat", ProviderID: "provider-primary", UpstreamModel: "upstream-primary", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_primary-chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "fallback-chat", ProviderID: "provider-fallback", UpstreamModel: "upstream-fallback", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_fallback-chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"primary-chat","models":["fallback-chat"],"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "ok from fallback") {
		t.Fatalf("expected fallback response, got %s", body)
	}
	if atomic.LoadInt32(&primaryCalls) != 1 || atomic.LoadInt32(&fallbackCalls) != 1 {
		t.Fatalf("unexpected upstream calls primary=%d fallback=%d", primaryCalls, fallbackCalls)
	}
	if usage := lastUsage(t, store); usage.ProviderID != "provider-fallback" || usage.UpstreamModel != "upstream-fallback" {
		t.Fatalf("unexpected fallback usage: %+v", usage)
	}
}

func TestGatewayProviderPreferencesFilterAndOrderCandidates(t *testing.T) {
	providerA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertModel(t, r, "upstream-a")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_a", "object": "chat.completion", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok from a"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}})
	}))
	defer providerA.Close()
	providerB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertModel(t, r, "upstream-b")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_b", "object": "chat.completion", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok from b"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}})
	}))
	defer providerB.Close()
	providerC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertModel(t, r, "upstream-c")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_c", "object": "chat.completion", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok from c"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}})
	}))
	defer providerC.Close()

	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "test-key", Name: "test", KeyHash: auth.HashAPIKey(testAPIKey), Prefix: auth.Prefix(testAPIKey), UserID: "user-1", OrganizationID: "org-1", ProjectID: "proj-1", Status: domain.StatusActive, AllowedProtocols: []string{domain.ProtocolOpenAI}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	providers := []domain.Provider{
		{ID: "provider-a", Name: "Provider A", BaseURL: providerA.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-a", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10},
		{ID: "provider-b", Name: "Provider B", BaseURL: providerB.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-b", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10},
		{ID: "provider-c", Name: "Provider C", BaseURL: providerC.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-c", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10},
	}
	for _, provider := range providers {
		if err := store.UpsertProvider(ctx, provider); err != nil {
			t.Fatal(err)
		}
	}
	pricingRules := []domain.PricingRule{
		{ID: "price_primary-openai", ModelAlias: "primary-openai", ProviderID: "provider-a", Currency: "USD", InputPricePerMillionTokens: 500000000, OutputPricePerMillionTokens: 1000000000},
		{ID: "price_fallback-cheap", ModelAlias: "fallback-cheap", ProviderID: "provider-b", Currency: "USD", InputPricePerMillionTokens: 100000000, OutputPricePerMillionTokens: 200000000},
		{ID: "price_fallback-dual", ModelAlias: "fallback-dual", ProviderID: "provider-c", Currency: "USD", InputPricePerMillionTokens: 90000000, OutputPricePerMillionTokens: 180000000},
	}
	for _, rule := range pricingRules {
		if err := store.UpsertPricingRule(ctx, rule); err != nil {
			t.Fatal(err)
		}
	}
	routes := []domain.ModelRoute{
		{PublicName: "primary-openai", ProviderID: "provider-a", UpstreamModel: "upstream-a", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_primary-openai", Enabled: true, Priority: 5, Weight: 10},
		{PublicName: "fallback-cheap", ProviderID: "provider-b", UpstreamModel: "upstream-b", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_fallback-cheap", Enabled: true, Priority: 3, Weight: 50},
		{PublicName: "fallback-dual", ProviderID: "provider-c", UpstreamModel: "upstream-c", Protocol: domain.ProtocolOpenAI, AllowedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}, ProxyProfileID: "direct", PriceRuleID: "price_fallback-dual", Enabled: true, Priority: 1, Weight: 100},
	}
	for _, route := range routes {
		if err := store.UpsertModelRoute(ctx, route); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	t.Run("only and order", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"primary-openai","models":["fallback-cheap","fallback-dual"],"messages":[{"role":"user","content":"hi"}],"provider":{"only":["provider-c","provider-b"],"order":["provider-c","provider-b"]}}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		body, _ := io.ReadAll(response.Body)
		if !strings.Contains(string(body), "ok from c") {
			t.Fatalf("expected provider-c response, got %s", body)
		}
		if usage := lastUsage(t, store); usage.ProviderID != "provider-c" {
			t.Fatalf("unexpected usage: %+v", usage)
		}
	})

	t.Run("ignore and sort by price", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"primary-openai","models":["fallback-cheap","fallback-dual"],"messages":[{"role":"user","content":"hi"}],"provider":{"ignore":["provider-a"],"sort":"price"}}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		body, _ := io.ReadAll(response.Body)
		if !strings.Contains(string(body), "ok from c") {
			t.Fatalf("expected cheapest provider-c response, got %s", body)
		}
		if usage := lastUsage(t, store); usage.ProviderID != "provider-c" {
			t.Fatalf("unexpected usage: %+v", usage)
		}
	})

	t.Run("max price and require parameters", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"primary-openai","models":["fallback-cheap","fallback-dual"],"messages":[{"role":"user","content":"hi"}],"top_k":5,"provider":{"require_parameters":true,"max_price":{"prompt":0.000001,"completion":0.000002}}}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		body, _ := io.ReadAll(response.Body)
		if !strings.Contains(string(body), "ok from c") {
			t.Fatalf("expected dual-protocol provider-c response, got %s", body)
		}
		if usage := lastUsage(t, store); usage.ProviderID != "provider-c" {
			t.Fatalf("unexpected usage: %+v", usage)
		}
	})

	t.Run("unsupported privacy preference is rejected", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"primary-openai","messages":[{"role":"user","content":"hi"}],"provider":{"zdr":true}}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
	})
}

func TestGatewayKeyInfoAndGenerationLookup(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			assertModel(t, r, "upstream-chat")
			writeJSON(t, w, map[string]any{"id": "chatcmpl_test", "object": "chat.completion", "model": "upstream-chat", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10}})
		case "/v1/embeddings":
			assertModel(t, r, "upstream-chat")
			writeJSON(t, w, map[string]any{"object": "list", "data": []any{}, "usage": map[string]any{"prompt_tokens": 4, "total_tokens": 4}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	chat := doRequest(t, server.URL+"/api/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer chat.Body.Close()
	if chat.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(chat.Body)
		t.Fatalf("chat status=%d body=%s", chat.StatusCode, body)
	}
	generationID := chat.Header.Get("X-Generation-Id")
	if generationID == "" {
		t.Fatal("missing X-Generation-Id header")
	}

	embeddings := doRequest(t, server.URL+"/api/v1/embeddings", http.MethodPost, []byte(`{"model":"public-chat","input":"hello"}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer embeddings.Body.Close()
	if embeddings.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(embeddings.Body)
		t.Fatalf("embeddings status=%d body=%s", embeddings.StatusCode, body)
	}

	generation := doRequest(t, server.URL+"/api/v1/generation?id="+generationID, http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer generation.Body.Close()
	if generation.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(generation.Body)
		t.Fatalf("generation status=%d body=%s", generation.StatusCode, body)
	}
	var generationPayload struct {
		Data struct {
			ID             string `json:"id"`
			ProviderID     string `json:"provider_id"`
			ModelAlias     string `json:"model_alias"`
			UpstreamModel  string `json:"upstream_model"`
			TotalTokens    int    `json:"total_tokens"`
			CostMicroCents int64  `json:"cost_micro_cents"`
			StatusCode     int    `json:"status_code"`
		} `json:"data"`
	}
	if err := json.NewDecoder(generation.Body).Decode(&generationPayload); err != nil {
		t.Fatal(err)
	}
	if generationPayload.Data.ID != generationID || generationPayload.Data.ProviderID != "openai-test" || generationPayload.Data.ModelAlias != "public-chat" || generationPayload.Data.UpstreamModel != "upstream-chat" || generationPayload.Data.TotalTokens != 10 || generationPayload.Data.CostMicroCents != 4750 || generationPayload.Data.StatusCode != http.StatusOK {
		t.Fatalf("unexpected generation payload: %+v", generationPayload.Data)
	}

	keyInfo := doRequest(t, server.URL+"/api/v1/key", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer keyInfo.Body.Close()
	if keyInfo.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(keyInfo.Body)
		t.Fatalf("key status=%d body=%s", keyInfo.StatusCode, body)
	}
	var keyPayload struct {
		Data struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Usage  struct {
				AllTime struct {
					Requests       int64 `json:"requests"`
					TotalTokens    int64 `json:"total_tokens"`
					CostMicroCents int64 `json:"cost_micro_cents"`
				} `json:"all_time"`
				Daily struct {
					Requests int64 `json:"requests"`
				} `json:"daily"`
				Weekly struct {
					Requests int64 `json:"requests"`
				} `json:"weekly"`
				Monthly struct {
					Requests int64 `json:"requests"`
				} `json:"monthly"`
			} `json:"usage"`
			Limits struct {
				RPM          int   `json:"rpm"`
				TPM          int   `json:"tpm"`
				DailyCents   int64 `json:"daily_budget_cents"`
				MonthlyCents int64 `json:"monthly_budget_cents"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.NewDecoder(keyInfo.Body).Decode(&keyPayload); err != nil {
		t.Fatal(err)
	}
	if keyPayload.Data.Name != "test" || keyPayload.Data.Status != domain.StatusActive {
		t.Fatalf("unexpected key payload: %+v", keyPayload.Data)
	}
	if keyPayload.Data.Usage.AllTime.Requests != 2 || keyPayload.Data.Usage.AllTime.TotalTokens != 14 || keyPayload.Data.Usage.AllTime.CostMicroCents != 5750 {
		t.Fatalf("unexpected key usage summary: %+v", keyPayload.Data.Usage.AllTime)
	}
	if keyPayload.Data.Usage.Daily.Requests != 2 || keyPayload.Data.Usage.Weekly.Requests != 2 || keyPayload.Data.Usage.Monthly.Requests != 2 {
		t.Fatalf("unexpected key window summaries: daily=%+v weekly=%+v monthly=%+v", keyPayload.Data.Usage.Daily, keyPayload.Data.Usage.Weekly, keyPayload.Data.Usage.Monthly)
	}

	missingGeneration := doRequest(t, server.URL+"/api/v1/generation", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer missingGeneration.Body.Close()
	if missingGeneration.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(missingGeneration.Body)
		t.Fatalf("missing generation status=%d body=%s", missingGeneration.StatusCode, body)
	}
}

func TestGatewayReturnsRouterMetadataWhenRequested(t *testing.T) {
	primaryUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertModel(t, r, "upstream-primary")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"primary unavailable"}}`))
	}))
	defer primaryUpstream.Close()

	fallbackUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertModel(t, r, "upstream-fallback")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_meta", "object": "chat.completion", "model": "upstream-fallback", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok from fallback"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 6, "completion_tokens": 2, "total_tokens": 8}})
	}))
	defer fallbackUpstream.Close()

	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "test-key", Name: "test", KeyHash: auth.HashAPIKey(testAPIKey), Prefix: auth.Prefix(testAPIKey), UserID: "user-1", OrganizationID: "org-1", ProjectID: "proj-1", Status: domain.StatusActive, AllowedProtocols: []string{domain.ProtocolOpenAI}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "provider-primary", Name: "Primary", BaseURL: primaryUpstream.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-primary", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "provider-fallback", Name: "Fallback", BaseURL: fallbackUpstream.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-fallback", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_primary-chat", ModelAlias: "primary-chat", ProviderID: "provider-primary", Currency: "USD", InputPricePerMillionTokens: 100000000, OutputPricePerMillionTokens: 200000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_fallback-chat", ModelAlias: "fallback-chat", ProviderID: "provider-fallback", Currency: "USD", InputPricePerMillionTokens: 80000000, OutputPricePerMillionTokens: 150000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "primary-chat", ProviderID: "provider-primary", UpstreamModel: "upstream-primary", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_primary-chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "fallback-chat", ProviderID: "provider-fallback", UpstreamModel: "upstream-fallback", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_fallback-chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"primary-chat","models":["fallback-chat"],"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey, "X-OpenRouter-Experimental-Metadata": "enabled"})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	var payload struct {
		Model          string `json:"model"`
		AIYoloMetadata struct {
			Requested     string `json:"requested"`
			ResolvedModel string `json:"resolved_model"`
			Strategy      string `json:"strategy"`
			Attempt       int    `json:"attempt"`
			Summary       string `json:"summary"`
			Endpoints     struct {
				Total     int `json:"total"`
				Available []struct {
					Provider string `json:"provider"`
					Model    string `json:"model"`
					Selected bool   `json:"selected"`
				} `json:"available"`
			} `json:"endpoints"`
			Attempts []struct {
				Index        int    `json:"index"`
				Provider     string `json:"provider"`
				Model        string `json:"model"`
				Status       string `json:"status"`
				StatusCode   int    `json:"status_code"`
				FailureClass string `json:"failure_class"`
			} `json:"attempts"`
		} `json:"aiyolo_metadata"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.AIYoloMetadata.Requested != "primary-chat" || payload.AIYoloMetadata.ResolvedModel != "upstream-fallback" || payload.AIYoloMetadata.Attempt != 2 || payload.AIYoloMetadata.Endpoints.Total != 2 || len(payload.AIYoloMetadata.Attempts) != 2 {
		t.Fatalf("unexpected metadata payload: %+v", payload.AIYoloMetadata)
	}
	if payload.AIYoloMetadata.Attempts[0].Status != "failed" || payload.AIYoloMetadata.Attempts[0].FailureClass != "upstream_5xx" {
		t.Fatalf("unexpected first attempt metadata: %+v", payload.AIYoloMetadata.Attempts[0])
	}
	if payload.AIYoloMetadata.Attempts[1].Status != "success" {
		t.Fatalf("unexpected second attempt metadata: %+v", payload.AIYoloMetadata.Attempts[1])
	}
}

func TestGatewaySerializesProtocolSpecificErrors(t *testing.T) {
	store := testStore(t, "http://127.0.0.1:1")
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	t.Run("openai error envelope", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"public-chat"}`), nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		var payload struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Error.Code != "missing_api_key" || payload.Error.Type != "invalid_request_error" || payload.Error.Message == "" {
			t.Fatalf("unexpected openai error payload: %+v", payload.Error)
		}
	})

	t.Run("anthropic error envelope", func(t *testing.T) {
		response := doRequest(t, server.URL+"/v1/messages", http.MethodPost, []byte(`{"model":"claude-public","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`), map[string]string{"anthropic-version": "2023-06-01"})
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		var payload struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type != "error" || payload.Error.Type != "authentication_error" || payload.Error.Message == "" {
			t.Fatalf("unexpected anthropic error payload: %+v", payload)
		}
	})
}

func TestGatewayResponseCacheNonStreaming(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		assertModel(t, r, "upstream-chat")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_cache", "object": "chat.completion", "model": "upstream-chat", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "cached response"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}})
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	headers := map[string]string{"Authorization": "Bearer " + testAPIKey, "X-OpenRouter-Cache": "true", "X-OpenRouter-Cache-TTL": "120"}
	body := []byte(`{"model":"public-chat","messages":[{"role":"user","content":"cache me"}]}`)

	first := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, body, headers)
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK || first.Header.Get("X-OpenRouter-Cache-Status") != "MISS" || first.Header.Get("X-OpenRouter-Cache-TTL") != "120" {
		payload, _ := io.ReadAll(first.Body)
		t.Fatalf("first request status=%d cache_status=%s ttl=%s body=%s", first.StatusCode, first.Header.Get("X-OpenRouter-Cache-Status"), first.Header.Get("X-OpenRouter-Cache-TTL"), payload)
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("expected 1 upstream call, got %d", upstreamCalls.Load())
	}
	if usage := lastUsage(t, store); usage.TotalTokens != 7 || usage.CostMicroCents == 0 {
		t.Fatalf("unexpected miss usage: %+v", usage)
	}

	second := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, body, headers)
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK || second.Header.Get("X-OpenRouter-Cache-Status") != "HIT" || second.Header.Get("X-OpenRouter-Cache-TTL") != "120" {
		payload, _ := io.ReadAll(second.Body)
		t.Fatalf("second request status=%d cache_status=%s ttl=%s body=%s", second.StatusCode, second.Header.Get("X-OpenRouter-Cache-Status"), second.Header.Get("X-OpenRouter-Cache-TTL"), payload)
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("cache hit should not reach upstream, got %d calls", upstreamCalls.Load())
	}
	var cachedPayload struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(second.Body).Decode(&cachedPayload); err != nil {
		t.Fatal(err)
	}
	if cachedPayload.Usage.PromptTokens != 0 || cachedPayload.Usage.CompletionTokens != 0 || cachedPayload.Usage.TotalTokens != 0 {
		t.Fatalf("expected zeroed cached usage, got %+v", cachedPayload.Usage)
	}
	if usage := lastUsage(t, store); usage.TotalTokens != 0 || usage.CostMicroCents != 0 || usage.ProviderID != "openai-test" {
		t.Fatalf("unexpected hit usage: %+v", usage)
	}

	clearedHeaders := map[string]string{"Authorization": "Bearer " + testAPIKey, "X-OpenRouter-Cache": "true", "X-OpenRouter-Cache-TTL": "120", "X-OpenRouter-Cache-Clear": "true"}
	third := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, body, clearedHeaders)
	defer third.Body.Close()
	if third.StatusCode != http.StatusOK || third.Header.Get("X-OpenRouter-Cache-Status") != "MISS" {
		payload, _ := io.ReadAll(third.Body)
		t.Fatalf("third request status=%d cache_status=%s body=%s", third.StatusCode, third.Header.Get("X-OpenRouter-Cache-Status"), payload)
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("cache clear should force upstream, got %d calls", upstreamCalls.Load())
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

func TestGatewayAllowedModelsEnforced(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertModel(t, r, "upstream-chat")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_test", "object": "chat.completion", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3}})
	}))
	defer upstream.Close()

	store := testStore(t, upstream.URL)
	ctx := context.Background()
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openai-test", UpstreamModel: "upstream-chat", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_public-chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-4o", ProviderID: "openai-test", UpstreamModel: "upstream-chat", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_public-chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "test-key", Name: "codex-scoped", KeyHash: auth.HashAPIKey(testAPIKey), Prefix: auth.Prefix(testAPIKey), UserID: "user-1", OrganizationID: "org-1", ProjectID: "proj-1", Status: domain.StatusActive, AllowedProtocols: []string{domain.ProtocolOpenAI}, AllowedModels: []string{"gpt-5.4", "gpt-5.5", "gpt-5.5-pro"}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	allowed := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer allowed.Body.Close()
	if allowed.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(allowed.Body)
		t.Fatalf("allowed status=%d body=%s", allowed.StatusCode, body)
	}

	blocked := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + testAPIKey})
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(blocked.Body)
		t.Fatalf("blocked status=%d body=%s", blocked.StatusCode, body)
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_public-chat", ModelAlias: "public-chat", ProviderID: "openai-test", Currency: "USD", InputPricePerMillionTokens: 250000000, OutputPricePerMillionTokens: 1000000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "public-chat", ProviderID: "openai-test", UpstreamModel: "upstream-chat", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", PriceRuleID: "price_public-chat", Enabled: true, ContextTokens: 128000}); err != nil {
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
