package console_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/app"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestConsoleLoginAndCreateAPIKey(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	loginForm := url.Values{"email": {"admin@example.com"}, "password": {"password"}}
	response, err := client.PostForm(server.URL+"/console/login", loginForm)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status=%d", response.StatusCode)
	}
	var session *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == "aiyolo_console" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("session cookie missing")
	}

	form := url.Values{"name": {"dev key"}, "kind": {"test"}, "allowed_protocols": {"openai,anthropic"}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/api-keys", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	created, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer created.Body.Close()
	if created.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(created.Body)
		t.Fatalf("create status=%d body=%s", created.StatusCode, body)
	}
	body, _ := io.ReadAll(created.Body)
	if !strings.Contains(string(body), "aiyolo_test_") {
		t.Fatalf("created key was not displayed once: %s", body)
	}
	keys, err := store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "dev key" || keys[0].KeyHash == "" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
}

func TestConsoleRotateAndDisableAPIKey(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	loginForm := url.Values{"email": {"admin@example.com"}, "password": {"password"}}
	response, err := client.PostForm(server.URL+"/console/login", loginForm)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status=%d", response.StatusCode)
	}
	var session *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == "aiyolo_console" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("session cookie missing")
	}

	createForm := url.Values{"name": {"rotating key"}, "kind": {"test"}}
	createRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/api-keys", strings.NewReader(createForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	createRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRequest.AddCookie(session)
	created, err := client.Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer created.Body.Close()
	if created.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(created.Body)
		t.Fatalf("create status=%d body=%s", created.StatusCode, body)
	}

	keys, err := store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	original := keys[0]

	rotateRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/api-keys/"+original.ID+"/rotate", nil)
	if err != nil {
		t.Fatal(err)
	}
	rotateRequest.AddCookie(session)
	rotatedResponse, err := client.Do(rotateRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer rotatedResponse.Body.Close()
	if rotatedResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(rotatedResponse.Body)
		t.Fatalf("rotate status=%d body=%s", rotatedResponse.StatusCode, body)
	}
	rotatedBody, _ := io.ReadAll(rotatedResponse.Body)
	if !strings.Contains(string(rotatedBody), "aiyolo_test_") {
		t.Fatalf("rotated clear key not shown: %s", rotatedBody)
	}
	keys, err = store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].KeyHash == original.KeyHash || keys[0].Status != domain.StatusActive {
		t.Fatalf("unexpected rotated key: %+v", keys)
	}
	rotated := keys[0]

	disableRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/api-keys/"+rotated.ID+"/disable", nil)
	if err != nil {
		t.Fatal(err)
	}
	disableRequest.AddCookie(session)
	disabledResponse, err := client.Do(disableRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer disabledResponse.Body.Close()
	if disabledResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(disabledResponse.Body)
		t.Fatalf("disable status=%d body=%s", disabledResponse.StatusCode, body)
	}
	keys, err = store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Status != domain.StatusDisabled || keys[0].KeyHash != rotated.KeyHash {
		t.Fatalf("unexpected disabled key: %+v", keys)
	}
}

func TestConsoleOAuthLoginAfterSavingSettings(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	oauthProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			redirectURI := r.URL.Query().Get("redirect_uri")
			state := r.URL.Query().Get("state")
			if redirectURI == "" || state == "" {
				t.Fatalf("missing redirect_uri or state: %s", r.URL.RawQuery)
			}
			http.Redirect(w, r, redirectURI+"?code=test-code&state="+url.QueryEscape(state), http.StatusFound)
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("client_id") != "client-id" || r.FormValue("client_secret") != "client-secret" || r.FormValue("code") != "test-code" {
				t.Fatalf("unexpected token form: %v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"oauth-access-token","token_type":"Bearer"}`))
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer oauth-access-token" {
				t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sub":"oauth-user","email":"admin@example.com","name":"Admin","login":"admin"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauthProvider.Close()

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	adminJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	adminClient := &http.Client{Jar: adminJar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	loginForm := url.Values{"email": {"admin@example.com"}, "password": {"password"}}
	loginResponse, err := adminClient.PostForm(server.URL+"/console/login", loginForm)
	if err != nil {
		t.Fatal(err)
	}
	defer loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin login status=%d", loginResponse.StatusCode)
	}

	settingsForm := url.Values{
		"local_password_enabled":                      {"on"},
		"allowed_emails":                              {"admin@example.com"},
		"provider_custom-oauth_enabled":               {"on"},
		"provider_custom-oauth_client_id":             {"client-id"},
		"provider_custom-oauth_client_secret":         {"client-secret"},
		"provider_custom-oauth_scopes":                {"openid,email,profile"},
		"provider_custom-oauth_auth_url":              {oauthProvider.URL + "/authorize"},
		"provider_custom-oauth_token_url":             {oauthProvider.URL + "/token"},
		"provider_custom-oauth_userinfo_url":          {oauthProvider.URL + "/userinfo"},
		"provider_custom-oauth_kind":                  {"oauth2"},
		"provider_custom-oauth_token_style":           {"form"},
		"provider_custom-oauth_token_response_path":   {"access_token"},
		"provider_custom-oauth_auth_style":            {"params"},
		"provider_custom-oauth_userinfo_method":       {"GET"},
		"provider_custom-oauth_userinfo_token_style":  {"bearer"},
		"provider_custom-oauth_userinfo_subject_path": {"sub"},
		"provider_custom-oauth_userinfo_email_path":   {"email"},
		"provider_custom-oauth_userinfo_name_path":    {"name"},
		"provider_custom-oauth_userinfo_login_path":   {"login"},
	}
	settingsRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/settings/auth", strings.NewReader(settingsForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	settingsRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settingsRequest.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	settingsResponse, err := adminClient.Do(settingsRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer settingsResponse.Body.Close()
	if settingsResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(settingsResponse.Body)
		t.Fatalf("save settings status=%d body=%s", settingsResponse.StatusCode, body)
	}
	settingsBody, _ := io.ReadAll(settingsResponse.Body)
	if !strings.Contains(string(settingsBody), "认证设置已保存") {
		t.Fatalf("settings save confirmation missing: %s", settingsBody)
	}

	guestJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	guestClient := &http.Client{Jar: guestJar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	loginPageRequest, err := http.NewRequest(http.MethodGet, server.URL+"/console/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	loginPageRequest.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	loginPage, err := guestClient.Do(loginPageRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer loginPage.Body.Close()
	loginPageBody, _ := io.ReadAll(loginPage.Body)
	if !strings.Contains(string(loginPageBody), "使用 通用 OAuth2 登录") {
		t.Fatalf("oauth login button missing: %s", loginPageBody)
	}

	startResponse, err := guestClient.Get(server.URL + "/console/login/custom-oauth")
	if err != nil {
		t.Fatal(err)
	}
	defer startResponse.Body.Close()
	if startResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("oauth start status=%d", startResponse.StatusCode)
	}
	authorizeLocation := startResponse.Header.Get("Location")
	if !strings.HasPrefix(authorizeLocation, oauthProvider.URL+"/authorize") {
		t.Fatalf("unexpected authorize location: %s", authorizeLocation)
	}

	authorizeResponse, err := guestClient.Get(authorizeLocation)
	if err != nil {
		t.Fatal(err)
	}
	defer authorizeResponse.Body.Close()
	if authorizeResponse.StatusCode != http.StatusFound {
		t.Fatalf("authorize status=%d", authorizeResponse.StatusCode)
	}
	callbackLocation := authorizeResponse.Header.Get("Location")
	if !strings.HasPrefix(callbackLocation, server.URL+"/console/oauth/custom-oauth/callback") {
		t.Fatalf("unexpected callback location: %s", callbackLocation)
	}

	callbackResponse, err := guestClient.Get(callbackLocation)
	if err != nil {
		t.Fatal(err)
	}
	defer callbackResponse.Body.Close()
	if callbackResponse.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(callbackResponse.Body)
		t.Fatalf("callback status=%d body=%s", callbackResponse.StatusCode, body)
	}
	if callbackResponse.Header.Get("Location") != "/console/" {
		t.Fatalf("unexpected callback redirect: %s", callbackResponse.Header.Get("Location"))
	}

	dashboardResponse, err := guestClient.Get(server.URL + "/console/")
	if err != nil {
		t.Fatal(err)
	}
	defer dashboardResponse.Body.Close()
	if dashboardResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(dashboardResponse.Body)
		t.Fatalf("dashboard status=%d body=%s", dashboardResponse.StatusCode, body)
	}
	dashboardBody, _ := io.ReadAll(dashboardResponse.Body)
	if !strings.Contains(string(dashboardBody), "Dashboard") {
		t.Fatalf("dashboard body missing title: %s", dashboardBody)
	}
}

func TestConsoleModelsProviderSelectionFiltersUpstreamOptions(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", Protocol: "openai", MasterKey: "sk-or-test", Status: "enabled", TimeoutSeconds: 90}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "anthropic-main", Name: "Anthropic", BaseURL: "https://api.anthropic.com", Protocol: "anthropic", MasterKey: "sk-ant-test", Status: "enabled", TimeoutSeconds: 90}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []domain.ModelRoute{
		{PublicName: "openai/gpt-4.1-mini", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: "openai", Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "google/gemini-2.5-flash", ProviderID: "openrouter", UpstreamModel: "google/gemini-2.5-flash", Protocol: "openai", Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "claude-sonnet", ProviderID: "anthropic-main", UpstreamModel: "claude-sonnet-4-5", Protocol: "anthropic", Enabled: true, Priority: 1, Weight: 100},
	} {
		if err := store.UpsertModelRoute(ctx, route); err != nil {
			t.Fatal(err)
		}
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/console/models?provider_id=openrouter", nil)
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
		t.Fatalf("models status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, `<option value="openai/gpt-4.1-mini"></option>`) || !strings.Contains(html, `<option value="google/gemini-2.5-flash"></option>`) {
		t.Fatalf("openrouter upstream options missing: %s", html)
	}
	if !strings.Contains(html, "Context currently shows the saved value and may not be verified.") {
		t.Fatalf("saved context note missing: %s", html)
	}
	if !strings.Contains(html, "Saved context") {
		t.Fatalf("saved context label missing: %s", html)
	}
	if strings.Contains(html, `<option value="claude-sonnet-4-5"></option>`) {
		t.Fatalf("unexpected foreign provider upstream option in filtered datalist: %s", html)
	}
	if !strings.Contains(html, `option value="openrouter" selected`) {
		t.Fatalf("selected provider was not preserved: %s", html)
	}
}

func TestConsoleCanResyncModelsFromExistingOpenRouterProvider(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-or-test" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("HTTP-Referer") != "https://github.com/zltl/aiyolo" {
			t.Fatalf("unexpected referer: %s", r.Header.Get("HTTP-Referer"))
		}
		if r.Header.Get("X-Title") != "aiyolo" {
			t.Fatalf("unexpected title: %s", r.Header.Get("X-Title"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4.1-mini","top_provider":{"context_length":128000},"pricing":{"prompt":"0.0000025","completion":"0.00001","input_cache_read":"0.0000005","input_cache_write":"0.00000625"}},{"id":"openrouter/auto","context_length":2000000,"pricing":{"prompt":"0.00000015","completion":"0.0000006"}},{"id":"foreign/shared-model","context_length":64000,"pricing":{"prompt":"0.0000009","completion":"0.0000018"}}]}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: "xray-balancer-socks5", Name: "Xray Balancer", Type: domain.ProxyTypeSOCKS5, Endpoint: "127.0.0.1:1080", Status: domain.StatusEnabled, TimeoutSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: "openai", MasterKey: "sk-or-test", DefaultProxyID: "xray-balancer-socks5", Status: "enabled", TimeoutSeconds: 90}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "openai/gpt-4.1-mini", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: "openai", ProxyProfileID: "edge-socks", Enabled: false, Priority: 7, Weight: 35, ContextTokens: 4096}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "other-provider", Name: "Other", BaseURL: "https://example.com/v1", Protocol: "openai", MasterKey: "sk-other", Status: "enabled", TimeoutSeconds: 90}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "foreign/shared-model", ProviderID: "other-provider", UpstreamModel: "shared-model", Protocol: "openai", ProxyProfileID: "direct", Enabled: true, Priority: 3, Weight: 90, ContextTokens: 32000}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
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
	if !strings.Contains(pageHTML, `action="/console/providers/openrouter/sync-models"`) {
		t.Fatalf("resync action missing from providers page: %s", pageHTML)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/providers/openrouter/sync-models", nil)
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
	if !strings.Contains(html, "Re-imported 2 models from OpenRouter") || !strings.Contains(html, "kept 1 conflicting routes") {
		t.Fatalf("resync notice missing expected summary: %s", html)
	}

	updated, err := store.LookupModelRoute(ctx, "openai/gpt-4.1-mini")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderID != "openrouter" || updated.ProxyProfileID != "edge-socks" || updated.Enabled || updated.Priority != 7 || updated.Weight != 35 || updated.ContextTokens != 128000 || updated.PriceRuleID == "" {
		t.Fatalf("openrouter route was not preserved and refreshed correctly: %+v", updated)
	}
	updatedRule, err := store.GetPricingRule(ctx, updated.PriceRuleID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedRule.ProviderID != "openrouter" || updatedRule.ModelAlias != "openai/gpt-4.1-mini" || updatedRule.InputPricePerMillionTokens != 250000000 || updatedRule.OutputPricePerMillionTokens != 1000000000 || updatedRule.CacheReadPricePerMillionTokens != 50000000 || updatedRule.CacheWritePricePerMillionTokens != 625000000 {
		t.Fatalf("unexpected pricing rule for updated route: %+v", updatedRule)
	}
	imported, err := store.LookupModelRoute(ctx, "openrouter/auto")
	if err != nil {
		t.Fatal(err)
	}
	if imported.ProviderID != "openrouter" || imported.UpstreamModel != "openrouter/auto" || !imported.Enabled || imported.ContextTokens != 2000000 || imported.PriceRuleID == "" {
		t.Fatalf("new openrouter route was not imported correctly: %+v", imported)
	}
	importedRule, err := store.GetPricingRule(ctx, imported.PriceRuleID)
	if err != nil {
		t.Fatal(err)
	}
	if importedRule.ProviderID != "openrouter" || importedRule.ModelAlias != "openrouter/auto" || importedRule.InputPricePerMillionTokens != 15000000 || importedRule.OutputPricePerMillionTokens != 60000000 {
		t.Fatalf("unexpected pricing rule for imported route: %+v", importedRule)
	}
	conflicting, err := store.LookupModelRoute(ctx, "foreign/shared-model")
	if err != nil {
		t.Fatal(err)
	}
	if conflicting.ProviderID != "other-provider" || conflicting.ContextTokens != 32000 {
		t.Fatalf("conflicting route should have been kept intact: %+v", conflicting)
	}

	modelsResponse, err := client.Get(server.URL + "/console/models?provider_id=openrouter")
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
	if !strings.Contains(modelsHTML, "Pricing") || !strings.Contains(modelsHTML, "$0.1500 / 1M in") || !strings.Contains(modelsHTML, "$0.6000 / 1M out") {
		t.Fatalf("pricing details missing from models page: %s", modelsHTML)
	}
	if !strings.Contains(modelsHTML, "Use provider default · xray-balancer-socks5") {
		t.Fatalf("model form should expose provider default proxy fallback: %s", modelsHTML)
	}
	if !regexp.MustCompile(`(?s)<strong>openrouter/auto</strong>.*?<dt>Proxy</dt>\s*<dd>xray-balancer-socks5</dd>`).MatchString(modelsHTML) {
		t.Fatalf("imported openrouter route should render the effective provider default proxy: %s", modelsHTML)
	}
}

func TestConsoleModelRouteTestBox(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-or-test" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_test","object":"chat.completion","created":1710000000,"model":"openai/gpt-4.1-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok from openrouter test"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: "openai", MasterKey: "sk-or-test", Status: "enabled", TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_public-openrouter", ModelAlias: "public-openrouter", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "public-openrouter", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: "openai", PriceRuleID: "price_public-openrouter", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"test_public_name": {"public-openrouter"}, "test_prompt": {"say ok"}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/models/test", strings.NewReader(form.Encode()))
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
		t.Fatalf("test status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "ok from openrouter test") {
		t.Fatalf("test response missing assistant output: %s", html)
	}
	if !strings.Contains(html, "Test succeeded") {
		t.Fatalf("success message missing: %s", html)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("expected one usage record, got %d", len(usage))
	}
	record := usage[0]
	if record.UserID != "admin@example.com" || record.APIKeyID != "" || record.ProviderID != "openrouter" || record.ModelAlias != "public-openrouter" || record.Protocol != domain.ProtocolOpenAI || record.Endpoint != "/console/models/test" {
		t.Fatalf("unexpected usage identity: %+v", record)
	}
	if record.InputTokens != 8 || record.OutputTokens != 4 || record.TotalTokens != 12 || record.CostMicroCents != 16 || record.StatusCode != http.StatusOK {
		t.Fatalf("unexpected usage accounting: %+v", record)
	}
	audits, err := store.ListAudit(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audits))
	}
	audit := audits[0]
	if audit.RequestID != record.RequestID || audit.UserID != "admin@example.com" || audit.EventType != "console_model_test" || audit.StatusCode != http.StatusOK || audit.CostMicroCents != 16 {
		t.Fatalf("unexpected audit event: %+v", audit)
	}
}

func TestConsoleModelRouteTestFailureWritesUsageAndAudit(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"This model is not available in your region.","type":"region_blocked"}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: "openai", MasterKey: "sk-or-test", Status: "enabled", TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "public-openrouter", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: "openai", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"test_public_name": {"public-openrouter"}, "test_prompt": {"say ok"}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/models/test", strings.NewReader(form.Encode()))
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
		t.Fatalf("test status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "This model is not available in your region.") {
		t.Fatalf("test error missing from response: %s", html)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("expected one usage record, got %d", len(usage))
	}
	if usage[0].StatusCode != http.StatusForbidden || usage[0].CostMicroCents != 0 || usage[0].TotalTokens != 0 {
		t.Fatalf("unexpected failed usage record: %+v", usage[0])
	}
	audits, err := store.ListAudit(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audits))
	}
	if audits[0].EventType != "console_model_test" || audits[0].StatusCode != http.StatusForbidden || audits[0].ErrorCode != "region_blocked" {
		t.Fatalf("unexpected failed audit event: %+v", audits[0])
	}
}

func TestConsoleModelRouteTestBoxUsesConfiguredProxy(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Test-Proxy") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"This model is not available in your region."}}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-or-test" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("HTTP-Referer") != "https://github.com/zltl/aiyolo" {
			t.Fatalf("openrouter referer header missing after proxy: %s", r.Header.Get("HTTP-Referer"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_proxy","object":"chat.completion","created":1710000000,"model":"openai/gpt-4.1-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok through proxy"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`))
	}))
	defer providerBackend.Close()

	var proxyHits atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		targetURL := r.RequestURI
		if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
			targetURL = r.URL.String()
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			t.Fatalf("proxy build request: %v", err)
		}
		request.ContentLength = r.ContentLength
		request.Header = r.Header.Clone()
		request.Header.Set("X-Test-Proxy", "yes")

		response, err := http.DefaultTransport.RoundTrip(request)
		if err != nil {
			t.Fatalf("proxy round trip: %v", err)
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxyServer.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: "openai", MasterKey: "sk-or-test", Status: "enabled", TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: "edge-http", Name: "Edge HTTP", Type: domain.ProxyTypeHTTP, Endpoint: proxyServer.URL, Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "public-openrouter-proxy", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: "openai", ProxyProfileID: "edge-http", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"test_public_name": {"public-openrouter-proxy"}, "test_prompt": {"say ok"}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/models/test", strings.NewReader(form.Encode()))
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
		t.Fatalf("test status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "ok through proxy") {
		t.Fatalf("test response missing proxy output: %s", html)
	}
	if proxyHits.Load() == 0 {
		t.Fatal("expected test request to use configured proxy")
	}
}

func TestConsoleChatPageRunsConversationTurn(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-chat-test" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		payload := string(body)
		if !strings.Contains(payload, `"role":"system","content":"Keep answers grounded in the selected route."`) {
			t.Fatalf("system prompt missing from upstream payload: %s", payload)
		}
		if !strings.Contains(payload, `"role":"assistant","content":"Earlier reply about latency"`) {
			t.Fatalf("prior assistant turn missing from upstream payload: %s", payload)
		}
		if !strings.Contains(payload, `"role":"user","content":"How would you route failover?"`) {
			t.Fatalf("latest user message missing from upstream payload: %s", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_console","object":"chat.completion","created":1710000000,"model":"openai/gpt-4.1-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Route failover via the weighted provider list."},"finish_reason":"stop"}],"usage":{"prompt_tokens":21,"completion_tokens":9,"total_tokens":30}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_ops-chat", ModelAlias: "ops-chat", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "ops-chat", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_ops-chat", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"chat_public_name":     {"ops-chat"},
		"chat_system_prompt":   {"Keep answers grounded in the selected route."},
		"chat_draft":           {"How would you route failover?"},
		"chat_message_role":    {"user", "assistant"},
		"chat_message_content": {"What is the current route?", "Earlier reply about latency"},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat", strings.NewReader(form.Encode()))
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
		t.Fatalf("chat status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "Route failover via the weighted provider list.") {
		t.Fatalf("assistant output missing from chat html: %s", html)
	}
	if !strings.Contains(html, "Earlier reply about latency") {
		t.Fatalf("prior transcript missing from chat html: %s", html)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("expected one usage record, got %d", len(usage))
	}
	record := usage[0]
	if record.UserID != "admin@example.com" || record.ProviderID != "openrouter" || record.ModelAlias != "ops-chat" || record.Protocol != domain.ProtocolOpenAI || record.Endpoint != "/console/chat" {
		t.Fatalf("unexpected usage identity: %+v", record)
	}
	if record.InputTokens != 21 || record.OutputTokens != 9 || record.TotalTokens != 30 || record.CostMicroCents != 39 || record.StatusCode != http.StatusOK {
		t.Fatalf("unexpected usage accounting: %+v", record)
	}
	audits, err := store.ListAudit(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audits))
	}
	audit := audits[0]
	if audit.RequestID != record.RequestID || audit.UserID != "admin@example.com" || audit.EventType != "console_chat" || audit.StatusCode != http.StatusOK || audit.CostMicroCents != 39 {
		t.Fatalf("unexpected audit event: %+v", audit)
	}
}

func TestConsoleChatPageFailureWritesUsageAndAudit(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited for this model","type":"rate_limit"}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "ops-chat", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"ops-chat"}, "chat_draft": {"How would you route failover?"}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat", strings.NewReader(form.Encode()))
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
		t.Fatalf("chat status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, "rate limited for this model") {
		t.Fatalf("chat error missing from response: %s", html)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("expected one usage record, got %d", len(usage))
	}
	if usage[0].StatusCode != http.StatusTooManyRequests || usage[0].CostMicroCents != 0 || usage[0].TotalTokens != 0 || usage[0].Endpoint != "/console/chat" {
		t.Fatalf("unexpected failed usage record: %+v", usage[0])
	}
	audits, err := store.ListAudit(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audits))
	}
	if audits[0].EventType != "console_chat" || audits[0].StatusCode != http.StatusTooManyRequests || audits[0].ErrorCode != "rate_limit" {
		t.Fatalf("unexpected failed audit event: %+v", audits[0])
	}
}

func TestConsoleChatStreamEndpointFlushesOpenAITurn(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-chat-stream" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"delta\":{\"content\":\"Route \"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"failover via weights.\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-stream", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_ops-stream", ModelAlias: "ops-stream", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "ops-stream", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_ops-stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"ops-stream"}, "chat_draft": {"How would you route failover?"}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/x-ndjson")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat stream status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	events := decodeConsoleChatStreamEvents(t, body)
	var deltaText strings.Builder
	var replaceHTML string
	for _, event := range events {
		if event.Type == "delta" {
			deltaText.WriteString(event.Delta)
		}
		if event.Type == "replace" {
			replaceHTML = event.HTML
		}
	}
	if deltaText.String() != "Route failover via weights." {
		t.Fatalf("unexpected streamed delta text: %q", deltaText.String())
	}
	if !strings.Contains(replaceHTML, "Route failover via weights.") {
		t.Fatalf("stream replacement html missing assistant output: %s", replaceHTML)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("expected one usage record, got %d", len(usage))
	}
	record := usage[0]
	if !record.Stream || record.Protocol != domain.ProtocolOpenAI || record.TotalTokens != 12 || record.CostMicroCents != 16 || record.Endpoint != "/console/chat" {
		t.Fatalf("unexpected streamed usage record: %+v", record)
	}
	audits, err := store.ListAudit(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || !audits[0].Stream || audits[0].EventType != "console_chat" {
		t.Fatalf("unexpected streamed audit records: %+v", audits)
	}
}

func TestConsoleChatPageListsAndStreamsAnthropicRoute(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "sk-ant-stream" {
			t.Fatalf("unexpected anthropic auth header: %s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Fatalf("unexpected anthropic version header: %s", r.Header.Get("anthropic-version"))
		}
		body, _ := io.ReadAll(r.Body)
		payload := string(body)
		if !strings.Contains(payload, `"system":"Keep replies short."`) {
			t.Fatalf("system prompt missing from anthropic payload: %s", payload)
		}
		if !strings.Contains(payload, `"role":"assistant","content":"Earlier Claude reply"`) {
			t.Fatalf("assistant history missing from anthropic payload: %s", payload)
		}
		if !strings.Contains(payload, `"role":"user","content":"Explain the current route."`) {
			t.Fatalf("latest user message missing from anthropic payload: %s", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_ant\",\"usage\":{\"input_tokens\":11}}}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Claude \"}}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"stream reply\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "anthropic-main", Name: "Anthropic", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolAnthropic, MasterKey: "sk-ant-stream", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_claude-stream", ModelAlias: "claude-sonnet", ProviderID: "anthropic-main", Currency: "USD", InputPricePerMillionTokens: 2000000, OutputPricePerMillionTokens: 4000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "claude-sonnet", ProviderID: "anthropic-main", UpstreamModel: "claude-sonnet-4-5", Protocol: domain.ProtocolAnthropic, AllowedProtocols: []string{domain.ProtocolAnthropic}, PriceRuleID: "price_claude-stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	pageResponse, err := client.Get(server.URL + "/console/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, "claude-sonnet") || !strings.Contains(pageHTML, "Anthropic") {
		t.Fatalf("anthropic route missing from chat page: %s", pageHTML)
	}

	form := url.Values{
		"chat_public_name":     {"claude-sonnet"},
		"chat_system_prompt":   {"Keep replies short."},
		"chat_draft":           {"Explain the current route."},
		"chat_message_role":    {"user", "assistant"},
		"chat_message_content": {"What route is active?", "Earlier Claude reply"},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/x-ndjson")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("anthropic chat stream status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	events := decodeConsoleChatStreamEvents(t, body)
	var deltaText strings.Builder
	var replaceHTML string
	for _, event := range events {
		if event.Type == "delta" {
			deltaText.WriteString(event.Delta)
		}
		if event.Type == "replace" {
			replaceHTML = event.HTML
		}
	}
	if deltaText.String() != "Claude stream reply" {
		t.Fatalf("unexpected anthropic streamed delta text: %q", deltaText.String())
	}
	if !strings.Contains(replaceHTML, "Claude stream reply") {
		t.Fatalf("anthropic replacement html missing assistant output: %s", replaceHTML)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("expected one usage record, got %d", len(usage))
	}
	record := usage[0]
	if !record.Stream || record.Protocol != domain.ProtocolAnthropic || record.TotalTokens != 18 || record.CostMicroCents != 50 || record.Endpoint != "/console/chat" {
		t.Fatalf("unexpected anthropic streamed usage record: %+v", record)
	}
	audits, err := store.ListAudit(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || !audits[0].Stream || audits[0].Protocol != domain.ProtocolAnthropic {
		t.Fatalf("unexpected anthropic streamed audit records: %+v", audits)
	}
}

func TestConsoleRejectsUnsupportedProxyType(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"id": {"bad-proxy"}, "name": {"Bad Proxy"}, "type": {"xray"}, "endpoint": {"127.0.0.1:10808"}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/proxies", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create proxy status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "unsupported proxy profile type") {
		t.Fatalf("unexpected validation message: %s", body)
	}
}

func TestConsoleProxyResourceCanBeEdited(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: "edge-socks", Name: "Edge SOCKS", Type: domain.ProxyTypeSOCKS5, Endpoint: "127.0.0.1:10808", Auth: "user:pass", Region: "sg", TimeoutSeconds: 75, HealthCheckURL: "https://probe.example.com/health", Status: domain.StatusDisabled}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	editRequest, err := http.NewRequest(http.MethodGet, server.URL+"/console/proxies?edit_proxy_id=edge-socks", nil)
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
		t.Fatalf("edit page status=%d body=%s", editResponse.StatusCode, body)
	}
	editBody, _ := io.ReadAll(editResponse.Body)
	html := string(editBody)
	if !strings.Contains(html, `name="id" value="edge-socks" readonly`) {
		t.Fatalf("proxy edit form did not load id: %s", html)
	}
	if !strings.Contains(html, `name="health_check_url" type="url" value="https://probe.example.com/health"`) {
		t.Fatalf("proxy edit form did not load health check url: %s", html)
	}
	if !strings.Contains(html, `option value="socks5" selected`) {
		t.Fatalf("proxy edit form did not select current type: %s", html)
	}
	if !strings.Contains(html, `name="endpoint" value="socks5://127.0.0.1:10808"`) {
		t.Fatalf("proxy edit form did not canonicalize socks5 endpoint: %s", html)
	}

	form := url.Values{
		"id":               {"edge-socks"},
		"name":             {"Edge SOCKS Updated"},
		"type":             {"socks5"},
		"endpoint":         {"127.0.0.1:20808"},
		"region":           {"jp"},
		"timeout_seconds":  {"80"},
		"health_check_url": {"https://probe.example.com/healthz"},
		"status":           {domain.StatusEnabled},
	}
	updateRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/proxies", strings.NewReader(form.Encode()))
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
		t.Fatalf("update proxy status=%d body=%s", updateResponse.StatusCode, body)
	}

	profile, err := store.GetProxyProfile(ctx, "edge-socks")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "Edge SOCKS Updated" || profile.Endpoint != "socks5://127.0.0.1:20808" || profile.Region != "jp" || profile.TimeoutSeconds != 80 || profile.HealthCheckURL != "https://probe.example.com/healthz" || profile.Status != domain.StatusEnabled {
		t.Fatalf("proxy was not updated: %+v", profile)
	}
	if profile.Auth != "user:pass" {
		t.Fatalf("proxy auth should be preserved, got %q", profile.Auth)
	}
}

func TestConsoleDirectProxyResourceCannotBeEdited(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	pageResponse, err := client.Get(server.URL + "/console/proxies")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if strings.Contains(pageHTML, `href="/console/proxies?edit_proxy_id=direct"`) {
		t.Fatalf("direct proxy should not expose an edit link: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, "Built-in direct, not editable") {
		t.Fatalf("direct proxy should be marked as locked: %s", pageHTML)
	}

	editRequest, err := http.NewRequest(http.MethodGet, server.URL+"/console/proxies?edit_proxy_id=direct", nil)
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
		t.Fatalf("direct edit page status=%d body=%s", editResponse.StatusCode, body)
	}
	editBody, _ := io.ReadAll(editResponse.Body)
	editHTML := string(editBody)
	if strings.Contains(editHTML, `name="id" value="direct" readonly`) {
		t.Fatalf("direct proxy should not load into edit mode: %s", editHTML)
	}
	if !strings.Contains(editHTML, "The built-in direct profile cannot be edited") {
		t.Fatalf("direct edit attempt should show an error: %s", editHTML)
	}

	form := url.Values{
		"id":              {"direct"},
		"name":            {"direct-updated"},
		"type":            {domain.ProxyTypeDirect},
		"timeout_seconds": {"99"},
		"status":          {domain.StatusDisabled},
	}
	updateRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/proxies", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	updateRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateResponse, err := client.Do(updateRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(updateResponse.Body)
		t.Fatalf("direct update status=%d body=%s", updateResponse.StatusCode, body)
	}
	updateBody, _ := io.ReadAll(updateResponse.Body)
	if !strings.Contains(string(updateBody), "The built-in direct profile cannot be edited") {
		t.Fatalf("unexpected direct update error: %s", updateBody)
	}

	profile, err := store.GetProxyProfile(ctx, domain.ProxyTypeDirect)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != domain.ProxyTypeDirect || profile.Status != domain.StatusEnabled || profile.TimeoutSeconds != 60 {
		t.Fatalf("direct proxy should remain unchanged: %+v", profile)
	}
}

func loggedInConsoleClient(serverURL string) (*http.Client, error) {
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

type consoleChatStreamTestEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	HTML  string `json:"html"`
}

func decodeConsoleChatStreamEvents(t *testing.T, body []byte) []consoleChatStreamTestEvent {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	events := make([]consoleChatStreamTestEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event consoleChatStreamTestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode stream event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}
