package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestFetchOpenRouterModelsUsesMetadataContextLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-or-test" {
			t.Fatalf("authorization=%q", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got != "https://github.com/zltl/aiyolo" {
			t.Fatalf("http-referer=%q", got)
		}
		if got := r.Header.Get("X-Title"); got != "aiyolo" {
			t.Fatalf("x-title=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-5.5","context_length":1050000,"pricing":{"prompt":"0.0000025","completion":"0.00001"},"top_provider":{"context_length":128000}},{"id":"openrouter/auto","pricing":{"prompt":"0.0000002","completion":"0.0000008"},"top_provider":{"context_length":2000000}},{"id":"provider/unknown","pricing":{"prompt":"0","completion":"0"}}]}`))
	}))
	defer server.Close()

	imports, err := fetchOpenRouterModels(context.Background(), domain.Provider{ID: "openrouter", BaseURL: server.URL + "/v1", MasterKey: "sk-or-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 3 {
		t.Fatalf("import count=%d", len(imports))
	}
	if imports[0].Route.PublicName != "openai/gpt-5.5" || imports[0].Route.ContextTokens != 1050000 || imports[0].PricingRule.InputPricePerMillionTokens != 250000000 || imports[0].PricingRule.OutputPricePerMillionTokens != 1000000000 {
		t.Fatalf("first import=%+v", imports[0])
	}
	if imports[1].Route.PublicName != "openrouter/auto" || imports[1].Route.ContextTokens != 2000000 {
		t.Fatalf("second import=%+v", imports[1])
	}
	if imports[2].Route.PublicName != "provider/unknown" || imports[2].Route.ContextTokens != 0 {
		t.Fatalf("third import=%+v", imports[2])
	}
}