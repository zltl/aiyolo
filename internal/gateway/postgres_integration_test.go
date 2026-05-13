package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/app"
	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestPostgresBackedGatewayFlow(t *testing.T) {
	databaseURL := os.Getenv("AIYOLO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AIYOLO_TEST_DATABASE_URL is not set")
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	publicModel := "pg-chat-" + suffix
	providerID := "pg-provider-" + suffix
	apiKey := "aiyolo_test_pg_" + suffix

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		assertHeader(t, r, "Authorization", "Bearer upstream-openai")
		assertModel(t, r, "upstream-chat")
		writeJSON(t, w, map[string]any{"id": "chatcmpl_pg", "object": "chat.completion", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
	}))
	defer upstream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := storage.OpenPostgres(ctx, databaseURL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: "direct", Name: "direct", Type: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: providerID, Name: "Postgres Integration", BaseURL: upstream.URL, Protocol: domain.ProtocolOpenAI, MasterKey: "upstream-openai", DefaultProxyID: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: publicModel, ProviderID: providerID, UpstreamModel: "upstream-chat", Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "pg-key-" + suffix, Name: "pg limited", KeyHash: auth.HashAPIKey(apiKey), Prefix: auth.Prefix(apiKey), UserID: "pg-user", OrganizationID: "pg-org", ProjectID: "pg-project", Status: domain.StatusActive, RPMLimit: 1, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(app.NewServer(testConfig(), store).Handler())
	defer server.Close()

	models := doRequest(t, server.URL+"/v1/models", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + apiKey})
	defer models.Body.Close()
	if models.StatusCode != http.StatusOK {
		t.Fatalf("models status=%d", models.StatusCode)
	}

	chat := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"`+publicModel+`","messages":[{"role":"user","content":"hi"}]}`), map[string]string{"Authorization": "Bearer " + apiKey})
	defer chat.Body.Close()
	if chat.StatusCode != http.StatusOK {
		t.Fatalf("chat status=%d", chat.StatusCode)
	}
	usage, err := store.ListUsage(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range usage {
		if item.ModelAlias == publicModel && item.TotalTokens == 5 && item.ProviderID == providerID {
			found = true
		}
	}
	if !found {
		encoded, _ := json.Marshal(usage)
		t.Fatalf("usage for %s was not written: %s", publicModel, encoded)
	}

	blocked := doRequest(t, server.URL+"/v1/chat/completions", http.MethodPost, []byte(`{"model":"`+publicModel+`","messages":[{"role":"user","content":"again"}]}`), map[string]string{"Authorization": "Bearer " + apiKey})
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("blocked status=%d", blocked.StatusCode)
	}
}
