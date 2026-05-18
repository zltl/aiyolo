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

func TestFetchCompatibleModelsSupportsDeepSeekList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-ds-test" {
			t.Fatalf("authorization=%q", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Fatalf("http-referer=%q, want empty", got)
		}
		if got := r.Header.Get("X-Title"); got != "" {
			t.Fatalf("x-title=%q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-v4-flash"},{"id":"deepseek-v4-pro","context_length":1048576},{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
	}))
	defer server.Close()

	imports, err := fetchCompatibleModels(context.Background(), domain.Provider{ID: "deepseek", BaseURL: server.URL, Protocol: domain.ProtocolOpenAI, SupportedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}, MasterKey: "sk-ds-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 4 {
		t.Fatalf("import count=%d", len(imports))
	}
	importedByID := make(map[string]openRouterImportedModel, len(imports))
	for _, imported := range imports {
		importedByID[imported.Route.PublicName] = imported
	}
	flash := importedByID["deepseek-v4-flash"]
	if flash.Route.PublicName != "deepseek-v4-flash" || flash.Route.Protocol != domain.ProtocolOpenAI {
		t.Fatalf("flash import=%+v", flash)
	}
	if len(flash.Route.AllowedProtocols) != 2 || flash.Route.AllowedProtocols[0] != domain.ProtocolOpenAI || flash.Route.AllowedProtocols[1] != domain.ProtocolAnthropic {
		t.Fatalf("flash protocols=%+v", flash.Route.AllowedProtocols)
	}
	if flash.Route.PriceRuleID == "" || flash.PricingRule.Currency != "CNY" || flash.PricingRule.InputPricePerMillionTokens != 100000000 || flash.PricingRule.OutputPricePerMillionTokens != 200000000 || flash.PricingRule.CacheReadPricePerMillionTokens != 2000000 || flash.PricingRule.CacheWritePricePerMillionTokens != 100000000 {
		t.Fatalf("unexpected flash pricing=%+v", flash)
	}
	pro := importedByID["deepseek-v4-pro"]
	if pro.Route.PublicName != "deepseek-v4-pro" || pro.Route.ContextTokens != 1048576 {
		t.Fatalf("pro import=%+v", pro)
	}
	if pro.Route.PriceRuleID == "" || pro.PricingRule.Currency != "CNY" || pro.PricingRule.InputPricePerMillionTokens != 300000000 || pro.PricingRule.OutputPricePerMillionTokens != 600000000 || pro.PricingRule.CacheReadPricePerMillionTokens != 2500000 || pro.PricingRule.CacheWritePricePerMillionTokens != 300000000 {
		t.Fatalf("unexpected pro pricing=%+v", pro)
	}
	for _, alias := range []string{"deepseek-chat", "deepseek-reasoner"} {
		imported := importedByID[alias]
		if imported.Route.PriceRuleID == "" || imported.PricingRule.Currency != "CNY" || imported.PricingRule.InputPricePerMillionTokens != 100000000 || imported.PricingRule.OutputPricePerMillionTokens != 200000000 || imported.PricingRule.CacheReadPricePerMillionTokens != 2000000 || imported.PricingRule.CacheWritePricePerMillionTokens != 100000000 {
			t.Fatalf("unexpected alias pricing for %s: %+v", alias, imported)
		}
	}
}