package console

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestProvidersPageCanResyncDeepSeekProvider(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-ds-test" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("HTTP-Referer") != "" {
			t.Fatalf("unexpected referer: %s", r.Header.Get("HTTP-Referer"))
		}
		if r.Header.Get("X-Title") != "" {
			t.Fatalf("unexpected title: %s", r.Header.Get("X-Title"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-v4-flash"},{"id":"deepseek-v4-pro","context_length":1048576},{"id":"shared-model"}]}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: providerBackend.URL, Protocol: domain.ProtocolOpenAI, SupportedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}, MasterKey: "sk-ds-test", Status: domain.StatusEnabled, TimeoutSeconds: 90}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "other-provider", Name: "Other", BaseURL: "https://example.com/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-other", Status: domain.StatusEnabled, TimeoutSeconds: 90}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "shared-model", ProviderID: "other-provider", UpstreamModel: "shared-model", Protocol: domain.ProtocolOpenAI, ProxyProfileID: domain.ProxyTypeDirect, Enabled: true, Priority: 3, Weight: 90, ContextTokens: 32000}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInBareConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	pageResponse, err := client.Get(server.URL + "/console/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, `action="/console/providers/deepseek"`) {
		t.Fatalf("deepseek quick start missing from providers page: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, `action="/console/providers/deepseek/sync-models"`) {
		t.Fatalf("deepseek resync action missing from providers page: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, "OpenAI / Anthropic") {
		t.Fatalf("deepseek dual protocol label missing from providers page: %s", pageHTML)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/providers/deepseek/sync-models", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("HX-Request", "true")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("resync status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "Re-imported 2 models from DeepSeek") || !strings.Contains(html, "kept 1 conflicting routes") {
		t.Fatalf("resync notice missing expected summary: %s", html)
	}

	imported, err := store.LookupModelRoute(ctx, "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	if imported.ProviderID != "deepseek" || imported.UpstreamModel != "deepseek-v4-pro" || !imported.Enabled || imported.ContextTokens != 1048576 {
		t.Fatalf("deepseek route was not imported correctly: %+v", imported)
	}
	if len(imported.AllowedProtocols) != 2 || imported.AllowedProtocols[0] != domain.ProtocolOpenAI || imported.AllowedProtocols[1] != domain.ProtocolAnthropic {
		t.Fatalf("unexpected deepseek allowed protocols: %+v", imported.AllowedProtocols)
	}
	if imported.PriceRuleID == "" {
		t.Fatalf("deepseek import should attach a pricing rule: %+v", imported)
	}
	pricingRule, err := store.GetPricingRule(ctx, imported.PriceRuleID)
	if err != nil {
		t.Fatal(err)
	}
	if pricingRule.Currency != "CNY" || pricingRule.InputPricePerMillionTokens != 300000000 || pricingRule.OutputPricePerMillionTokens != 600000000 || pricingRule.CacheReadPricePerMillionTokens != 2500000 || pricingRule.CacheWritePricePerMillionTokens != 300000000 {
		t.Fatalf("unexpected deepseek pricing rule: %+v", pricingRule)
	}

	modelsResponse, err := client.Get(server.URL + "/console/models?provider_id=deepseek")
	if err != nil {
		t.Fatal(err)
	}
	defer modelsResponse.Body.Close()
	if modelsResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(modelsResponse.Body)
		t.Fatalf("models status=%d body=%s", modelsResponse.StatusCode, body)
	}
	modelsBody, _ := io.ReadAll(modelsResponse.Body)
	modelsHTML := string(modelsBody)
	if !strings.Contains(modelsHTML, "¥3.0000 / 1M in") || !strings.Contains(modelsHTML, "¥6.0000 / 1M out") || !strings.Contains(modelsHTML, "¥0.0250 / 1M cache read") {
		t.Fatalf("deepseek pricing details missing from models page: %s", modelsHTML)
	}

	conflicting, err := store.LookupModelRoute(ctx, "shared-model")
	if err != nil {
		t.Fatal(err)
	}
	if conflicting.ProviderID != "other-provider" || conflicting.ContextTokens != 32000 {
		t.Fatalf("conflicting route should have been kept intact: %+v", conflicting)
	}
}

func TestProvidersPageCanEditExistingProvider(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: "edge-socks", Name: "Edge SOCKS", Type: domain.ProxyTypeSOCKS5, Endpoint: "socks5://127.0.0.1:10808", Status: domain.StatusEnabled, TimeoutSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Protocol: domain.ProtocolOpenAI, SupportedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}, MasterKey: "sk-ds-test", DefaultProxyID: "edge-socks", Priority: 7, Weight: 35, Status: domain.StatusDisabled, TimeoutSeconds: 120}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInBareConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	pageResponse, err := client.Get(server.URL + "/console/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, `href="/console/providers?edit_provider_id=deepseek"`) {
		t.Fatalf("provider edit link missing: %s", pageHTML)
	}

	editRequest, err := http.NewRequest(http.MethodGet, server.URL+"/console/providers?edit_provider_id=deepseek", nil)
	if err != nil {
		t.Fatal(err)
	}
	editRequest.Header.Set("HX-Request", "true")
	editResponse, err := client.Do(editRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer editResponse.Body.Close()
	if editResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(editResponse.Body)
		t.Fatalf("edit provider status=%d body=%s", editResponse.StatusCode, body)
	}
	editBody, _ := io.ReadAll(editResponse.Body)
	html := string(editBody)
	if !strings.Contains(html, `name="id" value="deepseek" readonly`) {
		t.Fatalf("provider edit form did not load id: %s", html)
	}
	if !strings.Contains(html, `name="base_url" type="url" value="https://api.deepseek.com"`) {
		t.Fatalf("provider edit form did not load base url: %s", html)
	}
	if !strings.Contains(html, `option value="openai" selected`) {
		t.Fatalf("provider edit form did not select primary protocol: %s", html)
	}
	if !strings.Contains(html, `name="supported_protocols" value="openai" checked`) || !strings.Contains(html, `name="supported_protocols" value="anthropic" checked`) {
		t.Fatalf("provider edit form did not load supported protocols: %s", html)
	}
	if !strings.Contains(html, `option value="edge-socks" selected`) {
		t.Fatalf("provider edit form did not load default proxy: %s", html)
	}
	if !strings.Contains(html, `name="timeout_seconds" type="number" min="0" value="120"`) {
		t.Fatalf("provider edit form did not load timeout: %s", html)
	}
	if !strings.Contains(html, `name="priority" type="number" value="7"`) || !strings.Contains(html, `name="weight" type="number" min="0" value="35"`) {
		t.Fatalf("provider edit form did not load scheduling fields: %s", html)
	}
	if !strings.Contains(html, `option value="disabled" selected`) {
		t.Fatalf("provider edit form did not load status: %s", html)
	}
	if !strings.Contains(html, "Leave blank to keep the saved master key") {
		t.Fatalf("provider edit form did not show saved master key hint: %s", html)
	}

	form := url.Values{
		"id":                  {"deepseek"},
		"name":                {"DeepSeek CN"},
		"base_url":            {"https://api.deepseek.com/v1"},
		"protocol":            {domain.ProtocolOpenAI},
		"supported_protocols": {domain.ProtocolOpenAI, domain.ProtocolAnthropic},
		"default_proxy_id":    {""},
		"timeout_seconds":     {"150"},
		"priority":            {"9"},
		"weight":              {"55"},
		"status":              {domain.StatusEnabled},
	}
	updateRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/providers", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	updateRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateResponse, err := client.Do(updateRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResponse.Body)
		t.Fatalf("update provider status=%d body=%s", updateResponse.StatusCode, body)
	}

	provider, err := store.GetProvider(ctx, "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name != "DeepSeek CN" || provider.BaseURL != "https://api.deepseek.com/v1" || provider.DefaultProxyID != "" || provider.TimeoutSeconds != 150 || provider.Priority != 9 || provider.Weight != 55 || provider.Status != domain.StatusEnabled {
		t.Fatalf("provider was not updated: %+v", provider)
	}
	if len(provider.SupportedProtocols) != 2 || provider.SupportedProtocols[0] != domain.ProtocolOpenAI || provider.SupportedProtocols[1] != domain.ProtocolAnthropic {
		t.Fatalf("provider supported protocols were not preserved: %+v", provider.SupportedProtocols)
	}
	if provider.MasterKey != "sk-ds-test" {
		t.Fatalf("provider master key should be preserved, got %q", provider.MasterKey)
	}
}

func loggedInBareConsoleClient(serverURL string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	loginForm := url.Values{"email": {"admin@example.com"}, "password": {"password"}}
	response, err := client.PostForm(serverURL+"/console/login", loginForm)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("login status=%d body=%s", response.StatusCode, body)
	}
	return client, nil
}