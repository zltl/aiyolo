package console_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/app"
	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/auth"
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

func TestConsoleLoginSessionCookieExpiresInThreeMonths(t *testing.T) {
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
			break
		}
	}
	if session == nil {
		t.Fatal("session cookie missing")
	}
	if session.Expires.IsZero() {
		t.Fatal("session cookie expiry missing")
	}
	remaining := time.Until(session.Expires)
	if remaining < 89*24*time.Hour || remaining > 91*24*time.Hour {
		t.Fatalf("session cookie remaining lifetime=%s, want about 90 days", remaining)
	}
	if session.MaxAge < int((89*24*time.Hour)/time.Second) {
		t.Fatalf("session cookie max_age=%d, want close to 90 days", session.MaxAge)
	}
}

func TestConsoleProtectedRequestRenewsSessionCookie(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	expiresAt := time.Now().Add(time.Hour).Unix()
	payload := "admin@example.com:" + strconv.FormatInt(expiresAt, 10)
	signature := auth.Sign(payload, cfg.SecretKey)
	request, err := http.NewRequest(http.MethodGet, server.URL+"/console/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "aiyolo_console", Value: payload + ":" + signature, Path: "/console"})

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("dashboard status=%d body=%s", response.StatusCode, body)
	}

	var refreshed *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == "aiyolo_console" {
			refreshed = cookie
			break
		}
	}
	if refreshed == nil {
		t.Fatal("refreshed session cookie missing")
	}
	if refreshed.Expires.Before(time.Unix(expiresAt, 0).Add(80 * 24 * time.Hour)) {
		t.Fatalf("refreshed cookie expiry=%s, want sliding renewal from prior expiry=%s", refreshed.Expires.UTC(), time.Unix(expiresAt, 0).UTC())
	}
	remaining := time.Until(refreshed.Expires)
	if remaining < 89*24*time.Hour || remaining > 91*24*time.Hour {
		t.Fatalf("refreshed cookie remaining lifetime=%s, want about 90 days", remaining)
	}
}

func TestConsoleChatPagePersistsSessionTurn(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_persist","choices":[{"index":0,"message":{"role":"assistant","content":"Route failover via the weighted provider list."},"finish_reason":"stop"}],"usage":{"prompt_tokens":21,"completion_tokens":9,"total_tokens":30}}`))
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_persist", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_persist", Enabled: true, Priority: 1, Weight: 100}); err != nil {
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
		"chat_client_session_id": {"session-persist"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_system_prompt":     {"Keep answers grounded in the selected route."},
		"chat_draft":             {"How would you route failover?"},
		"chat_message_role":      {"user", "assistant"},
		"chat_message_content":   {"What is the current route?", "Earlier reply about latency"},
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
	session, err := store.GetConsoleChatSession(ctx, "admin@example.com", "session-persist")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != "completed" {
		t.Fatalf("session status=%q", session.Status)
	}
	if session.MessageCount != 4 {
		t.Fatalf("session message_count=%d", session.MessageCount)
	}
	if !strings.Contains(session.MessagesJSON, "Earlier reply about latency") || !strings.Contains(session.MessagesJSON, "Route failover via the weighted provider list.") {
		t.Fatalf("unexpected session transcript: %s", session.MessagesJSON)
	}
	if session.LastRequestID == "" || session.LastResponseID != "chatcmpl_persist" {
		t.Fatalf("unexpected session ids: %+v", session)
	}
}

func TestConsoleChatPageLoadsPersistedServerSession(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	sessionUpdatedAt := time.Now().UTC()
	if err := store.UpsertConsoleChatSession(ctx, domain.ConsoleChatSession{
		ID:           "session-resume",
		UserID:       "admin@example.com",
		Title:        "Recovered thread",
		CustomTitle:  true,
		PublicName:   "gpt-5.4",
		SystemPrompt: "Stay grounded.",
		Status:       "completed",
		MessagesJSON: `[{"id":"msg_resume_user","role":"user","label":"You","content":"Resume me"},{"id":"msg_resume_assistant","role":"assistant","label":"AIYolo","content":"Recovered output"}]`,
		MessageCount: 2,
		CreatedAt:    sessionUpdatedAt,
		UpdatedAt:    sessionUpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Get(server.URL + "/console/chat")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("chat page status=%d body=%s", response.StatusCode, body)
	}
	if got := response.Header.Get("Location"); got != "/console/chat?session=session-resume" {
		response.Body.Close()
		t.Fatalf("unexpected canonical redirect location: %s", got)
	}
	response.Body.Close()

	response, err = client.Get(server.URL + "/console/chat?session=session-resume")
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
	if !strings.Contains(html, "Recovered output") {
		t.Fatalf("persisted assistant output missing from chat page: %s", html)
	}
	if !strings.Contains(html, "Recovered thread") {
		t.Fatalf("persisted session title missing from chat page: %s", html)
	}
	if !strings.Contains(html, `name="chat_client_session_id" value="session-resume"`) {
		t.Fatalf("persisted session id missing from chat page: %s", html)
	}
	if !strings.Contains(html, `id="chat-session-store-json"`) {
		t.Fatalf("server session store payload missing from chat page: %s", html)
	}
}

func TestConsoleChatPageRedirectsToCanonicalSessionQuery(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpsertConsoleChatSession(ctx, domain.ConsoleChatSession{
		ID:           "session-canonical",
		UserID:       "admin@example.com",
		Title:        "Canonical thread",
		CustomTitle:  true,
		PublicName:   "gpt-5.4",
		SystemPrompt: "Stay grounded.",
		Status:       "completed",
		MessagesJSON: `[{"id":"msg_canonical_user","role":"user","label":"You","content":"Canon"},{"id":"msg_canonical_assistant","role":"assistant","label":"AIYolo","content":"Canonical output"}]`,
		MessageCount: 2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	loginForm := url.Values{"email": {"admin@example.com"}, "password": {"password"}}
	loginResponse, err := client.PostForm(server.URL+"/console/login", loginForm)
	if err != nil {
		t.Fatal(err)
	}
	defer loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status=%d", loginResponse.StatusCode)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/console/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range loginResponse.Cookies() {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected redirect to canonical session URL, status=%d body=%s", response.StatusCode, body)
	}
	if got := response.Header.Get("Location"); got != "/console/chat?session=session-canonical" {
		t.Fatalf("unexpected canonical redirect location: %s", got)
	}
}

func TestConsoleChatPageLoadsRequestedServerSessionFromQuery(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpsertConsoleChatSession(ctx, domain.ConsoleChatSession{
		ID:           "session-newer",
		UserID:       "admin@example.com",
		Title:        "Newest thread",
		CustomTitle:  true,
		PublicName:   "gpt-5.4",
		SystemPrompt: "Stay grounded.",
		Status:       "completed",
		MessagesJSON: `[{"id":"msg_newer_user","role":"user","label":"You","content":"Newest"},{"id":"msg_newer_assistant","role":"assistant","label":"AIYolo","content":"Newest output"}]`,
		MessageCount: 2,
		CreatedAt:    now,
		UpdatedAt:    now.Add(1 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertConsoleChatSession(ctx, domain.ConsoleChatSession{
		ID:           "session-shared",
		UserID:       "admin@example.com",
		Title:        "Shared thread",
		CustomTitle:  true,
		PublicName:   "gpt-5.4",
		SystemPrompt: "Stay grounded.",
		Status:       "completed",
		MessagesJSON: `[{"id":"msg_shared_user","role":"user","label":"You","content":"Shared"},{"id":"msg_shared_assistant","role":"assistant","label":"AIYolo","content":"Shared output"}]`,
		MessageCount: 2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Get(server.URL + "/console/chat?session=session-shared")
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
	if !strings.Contains(html, "Shared output") {
		t.Fatalf("requested shared session output missing from chat page: %s", html)
	}
	if !strings.Contains(html, `&#34;activeSessionId&#34;:&#34;session-shared&#34;`) {
		t.Fatalf("requested active session missing from store payload: %s", html)
	}
	newerIndex := strings.Index(html, `&#34;id&#34;:&#34;session-newer&#34;`)
	sharedIndex := strings.Index(html, `&#34;id&#34;:&#34;session-shared&#34;`)
	if newerIndex == -1 || sharedIndex == -1 {
		t.Fatalf("session ids missing from store payload: %s", html)
	}
	if newerIndex > sharedIndex {
		t.Fatalf("requested older session should not move ahead of newer session in store payload: %s", html)
	}
	if !strings.Contains(html, `name="chat_client_session_id" value="session-shared"`) {
		t.Fatalf("requested session id missing from chat page: %s", html)
	}
	if strings.Contains(html, `/console/locale?lang=`) {
		t.Fatalf("locale switch should not be rendered anymore: %s", html)
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

func TestConsoleUpdateAPIKey(t *testing.T) {
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

	createForm := url.Values{"name": {"editable key"}, "kind": {"live"}}
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
	target := keys[0]

	updateForm := url.Values{
		"name":               {"edited key"},
		"allowed_protocols":  {"openai,anthropic"},
		"allowed_models":     {"gpt-5.4,gpt-image-2"},
		"rpm_limit":          {"120"},
		"tpm_limit":          {"34000"},
		"concurrent_limit":   {"5"},
		"daily_budget_cents": {"8000"},
		"monthly_budget_cents": {"120000"},
	}
	updateRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/api-keys/"+target.ID+"/update", strings.NewReader(updateForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	updateRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateRequest.AddCookie(session)
	updated, err := client.Do(updateRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Body.Close()
	if updated.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updated.Body)
		t.Fatalf("update status=%d body=%s", updated.StatusCode, body)
	}
	body, _ := io.ReadAll(updated.Body)
	if !strings.Contains(string(body), "API 密钥已更新") {
		t.Fatalf("update notice missing: %s", body)
	}
	if !strings.Contains(string(body), "编辑 API 密钥") {
		t.Fatalf("update should render api key edit page: %s", body)
	}

	keys, err = store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key after update, got %d", len(keys))
	}
	key := keys[0]
	if key.ID != target.ID || key.KeyHash != target.KeyHash || key.Prefix != target.Prefix {
		t.Fatalf("update should keep identity/hash/prefix unchanged: before=%+v after=%+v", target, key)
	}
	if key.Name != "edited key" || key.RPMLimit != 120 || key.TPMLimit != 34000 || key.ConcurrentLimit != 5 || key.DailyBudgetCents != 8000 || key.MonthlyBudgetCents != 120000 {
		t.Fatalf("unexpected updated limits: %+v", key)
	}
	if strings.Join(key.AllowedProtocols, ",") != "openai,anthropic" {
		t.Fatalf("unexpected allowed protocols: %+v", key.AllowedProtocols)
	}
	if strings.Join(key.AllowedModels, ",") != "gpt-5.4,gpt-image-2" {
		t.Fatalf("unexpected allowed models: %+v", key.AllowedModels)
	}
}

func TestConsoleAPIKeyEditPageShowsModelHints(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
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

	createForm := url.Values{"name": {"edit hint key"}, "kind": {"live"}, "allowed_models": {"custom/model-alpha"}}
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

	editRequest, err := http.NewRequest(http.MethodGet, server.URL+"/console/api-keys/"+keys[0].ID+"/edit", nil)
	if err != nil {
		t.Fatal(err)
	}
	editRequest.AddCookie(session)
	editResponse, err := client.Do(editRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer editResponse.Body.Close()
	if editResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(editResponse.Body)
		t.Fatalf("edit page status=%d body=%s", editResponse.StatusCode, body)
	}
	body, _ := io.ReadAll(editResponse.Body)
	html := string(body)
	if !strings.Contains(html, "编辑 API 密钥") {
		t.Fatalf("edit page heading missing: %s", html)
	}
	if !strings.Contains(html, `datalist id="api-key-edit-model-options"`) {
		t.Fatalf("model hints datalist missing: %s", html)
	}
	if !strings.Contains(html, `<option value="gpt-5.4"></option>`) {
		t.Fatalf("route alias hint missing: %s", html)
	}
	if !strings.Contains(html, `<option value="custom/model-alpha"></option>`) {
		t.Fatalf("saved model hint missing: %s", html)
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
	if !strings.Contains(string(dashboardBody), "总览") {
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
	if !strings.Contains(html, "当前显示的是已保存的上下文值，不保证已经核验。") {
		t.Fatalf("saved context note missing: %s", html)
	}
	if !strings.Contains(html, "已存上下文") {
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
	if !strings.Contains(html, "已从 OpenRouter 重新导入 2 个模型，并保留了 1 条同名路由。") {
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
	if updatedRule.ProviderID != "openrouter" || updatedRule.ModelAlias != "openai/gpt-4.1-mini" || updatedRule.Currency != domain.CurrencyCNY || updatedRule.InputPricePerMillionTokens != 2000000000 || updatedRule.OutputPricePerMillionTokens != 8000000000 || updatedRule.CacheReadPricePerMillionTokens != 400000000 || updatedRule.CacheWritePricePerMillionTokens != 5000000000 {
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
	if importedRule.ProviderID != "openrouter" || importedRule.ModelAlias != "openrouter/auto" || importedRule.Currency != domain.CurrencyCNY || importedRule.InputPricePerMillionTokens != 120000000 || importedRule.OutputPricePerMillionTokens != 480000000 {
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
	if !strings.Contains(modelsHTML, "计费") || !strings.Contains(modelsHTML, "¥1.2000 / 百万输入") || !strings.Contains(modelsHTML, "¥4.8000 / 百万输出") {
		t.Fatalf("pricing details missing from models page: %s", modelsHTML)
	}
	if !strings.Contains(modelsHTML, "继承提供方默认代理 · xray-balancer-socks5") {
		t.Fatalf("model form should expose provider default proxy fallback: %s", modelsHTML)
	}
	if !regexp.MustCompile(`(?s)<strong>openrouter/auto</strong>.*?<dt>代理</dt>\s*<dd>xray-balancer-socks5</dd>`).MatchString(modelsHTML) {
		t.Fatalf("imported openrouter route should render the effective provider default proxy: %s", modelsHTML)
	}
}

func TestConsoleSettingsExchangeRateAffectsOpenRouterSync(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-or-test" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4.1-mini","pricing":{"prompt":"0.0000025","completion":"0.00001"}}]}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	ctx := context.Background()
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-or-test", Status: domain.StatusEnabled, TimeoutSeconds: 90}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	settingsForm := url.Values{
		"local_password_enabled":   {"on"},
		"allowed_emails":           {"admin@example.com"},
		"usd_to_cny_exchange_rate": {"6.5"},
	}
	settingsRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/settings/auth", strings.NewReader(settingsForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	settingsRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settingsRequest.Header.Set("HX-Request", "true")
	settingsResponse, err := client.Do(settingsRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer settingsResponse.Body.Close()
	if settingsResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(settingsResponse.Body)
		t.Fatalf("save settings status=%d body=%s", settingsResponse.StatusCode, body)
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

	imported, err := store.LookupModelRoute(ctx, "openai/gpt-4.1-mini")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.GetPricingRule(ctx, imported.PriceRuleID)
	if err != nil {
		t.Fatal(err)
	}
	if rule.Currency != domain.CurrencyCNY || rule.InputPricePerMillionTokens != 1625000000 || rule.OutputPricePerMillionTokens != 6500000000 {
		t.Fatalf("unexpected pricing rule after custom exchange rate: %+v", rule)
	}

	settings, err := store.GetConsoleAuthSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.USDCNYExchangeRate != "6.5" {
		t.Fatalf("unexpected saved exchange rate: %+v", settings)
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
	if !strings.Contains(html, "测试成功，已从上游拿到响应。") {
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
}

func TestConsoleModelRouteTestFailureWritesUsage(t *testing.T) {
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

func TestConsoleChatPageIsPrimaryEntry(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.example/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-page", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "legacy-ops-chat", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 2, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
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
	if !strings.Contains(html, "chat-workspace-page") || !strings.Contains(html, "gpt-5.4") {
		t.Fatalf("chat primary entry missing expected content: %s", html)
	}
	if strings.Contains(html, `id="chat-advanced"`) || strings.Contains(html, `>高级设置<`) || strings.Contains(html, `>System prompt<`) {
		t.Fatalf("advanced chat settings should be omitted from chat page: %s", html)
	}
	if !strings.Contains(html, `<textarea name="chat_system_prompt" hidden>`) {
		t.Fatalf("chat page should keep a hidden system prompt field: %s", html)
	}
	if !strings.Contains(html, "你正在 AIYolo 提供的 AI 助手") {
		t.Fatalf("default production system prompt missing from chat page: %s", html)
	}
	if strings.Contains(html, "legacy-ops-chat") {
		t.Fatalf("disallowed route leaked into curated chat workspace: %s", html)
	}
}

func TestConsoleChatSessionSavePersistsDraftAttachmentsAndRestoresOnPageLoad(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.example/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-draft", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{
		HTTPAddr:      ":0",
		SecretKey:     "test-secret",
		AdminEmail:    "admin@example.com",
		AdminPassword: "password",
		ChatAttachments: artifacts.Config{
			PublicBaseURL: "https://chat-assets.example",
			ProxyBasePath: "/console/chat/attachments/files",
			S3: artifacts.S3Config{
				Endpoint:        "https://s3.example.com",
				Bucket:          "chat",
				Prefix:          "chat",
				AccessKeyID:     "key",
				AccessKeySecret: "secret",
			},
		},
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  5 * time.Second,
	}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"id":"session-draft","title":"gpt-5.4","customTitle":false,"publicName":"gpt-5.4","systemPrompt":"Stay grounded.","draft":"Sketch before send","draftAttachments":[{"id":"att-draft","name":"whiteboard.png","objectKey":"drafts/whiteboard.png","mediaType":"image/png","sizeBytes":128}],"messages":[]}`
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/session", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("save session status=%d body=%s", response.StatusCode, body)
	}
	session, err := store.GetConsoleChatSession(ctx, "admin@example.com", "session-draft")
	if err != nil {
		t.Fatal(err)
	}
	if session.Title != "新会话" {
		t.Fatalf("draft-only session title=%q", session.Title)
	}
	if session.Draft != "Sketch before send" {
		t.Fatalf("draft=%q", session.Draft)
	}
	if !strings.Contains(session.DraftAttachmentsJSON, "whiteboard.png") || !strings.Contains(session.DraftAttachmentsJSON, "drafts/whiteboard.png") {
		t.Fatalf("draft attachments not persisted: %s", session.DraftAttachmentsJSON)
	}

	pageResponse, err := client.Get(server.URL + "/console/chat")
	if err != nil {
		t.Fatal(err)
	}
	if pageResponse.StatusCode != http.StatusSeeOther {
		pageBody, _ := io.ReadAll(pageResponse.Body)
		pageResponse.Body.Close()
		t.Fatalf("expected canonical draft redirect, status=%d body=%s", pageResponse.StatusCode, pageBody)
	}
	if got := pageResponse.Header.Get("Location"); got != "/console/chat?session=session-draft" {
		pageResponse.Body.Close()
		t.Fatalf("unexpected draft redirect location: %s", got)
	}
	pageResponse.Body.Close()

	pageResponse, err = client.Get(server.URL + "/console/chat?session=session-draft")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, `name="chat_client_session_id" value="session-draft"`) {
		t.Fatalf("draft session id missing from page: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, "Sketch before send") {
		t.Fatalf("draft text missing from page: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, "whiteboard.png") {
		t.Fatalf("draft attachment missing from page: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, `class="chat-icon-button" type="button" data-chat-action="pick-attachments"`) {
		t.Fatalf("attachment action should render as an icon button: %s", pageHTML)
	}
}

func TestConsoleChatSessionSaveGeneratesAISummarizedTitle(t *testing.T) {
	var providerHits atomic.Int32
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		providerHits.Add(1)
		body, _ := io.ReadAll(r.Body)
		payload := string(body)
		if !strings.Contains(payload, `你负责给聊天会话生成标题。只输出一个简短标题，不要引号、序号、前缀、句号或额外解释。尽量使用对话本身的语言，控制在 12 个词以内。`) {
			t.Fatalf("title generation system prompt missing from upstream payload: %s", payload)
		}
		if !strings.Contains(payload, `How would you route failover?`) || !strings.Contains(payload, `Route failover via weighted providers.`) {
			t.Fatalf("conversation transcript missing from title generation payload: %s", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_title","choices":[{"index":0,"message":{"role":"assistant","content":"Title: Weighted route failover."},"finish_reason":"stop"}],"usage":{"prompt_tokens":18,"completion_tokens":4,"total_tokens":22}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-title", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"id":"session-title","title":"How would you route failover?","customTitle":false,"publicName":"gpt-5.4","systemPrompt":"Stay grounded.","draft":"","messages":[{"id":"msg-user","role":"user","label":"You","content":"How would you route failover?"},{"id":"msg-assistant","role":"assistant","label":"AIYolo","content":"Route failover via weighted providers."}]}`
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/session", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("save session status=%d body=%s", response.StatusCode, body)
	}
	var saved struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Weighted route failover" {
		t.Fatalf("response title=%q", saved.Title)
	}
	session, err := store.GetConsoleChatSession(ctx, "admin@example.com", "session-title")
	if err != nil {
		t.Fatal(err)
	}
	if session.Title != "Weighted route failover" {
		t.Fatalf("stored title=%q", session.Title)
	}
	if providerHits.Load() != 1 {
		t.Fatalf("expected one title generation request, got %d", providerHits.Load())
	}
}

func TestConsoleChatSessionSavePreservesUpdatedAtWithoutMessageActivity(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	messages := []map[string]any{
		{"id": "msg-user", "role": "user", "label": "You", "content": "How would you route failover?"},
		{"id": "msg-assistant", "role": "assistant", "label": "AIYolo", "content": "Route failover via weighted providers."},
	}
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	createdAt := updatedAt.Add(-45 * time.Minute)
	lastMessageAt := updatedAt
	if err := store.UpsertConsoleChatSession(ctx, domain.ConsoleChatSession{
		UserID:        "admin@example.com",
		ID:            "session-stable",
		Title:         "How would you route failover?",
		PublicName:    "gpt-5.4",
		SystemPrompt:  "Stay grounded.",
		MessagesJSON:  string(messagesJSON),
		MessageCount:  len(messages),
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		LastMessageAt: &lastMessageAt,
		Status:        "ready",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"id":           "session-stable",
		"title":        "How would you route failover?",
		"customTitle":  false,
		"publicName":   "gpt-5.4",
		"systemPrompt": "Stay grounded.",
		"draft":        "note later",
		"messages":     messages,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/session", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("save session status=%d body=%s", response.StatusCode, body)
	}
	var saved struct {
		UpdatedAt time.Time `json:"updatedAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if !saved.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("response updatedAt=%s want=%s", saved.UpdatedAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano))
	}

	session, err := store.GetConsoleChatSession(ctx, "admin@example.com", "session-stable")
	if err != nil {
		t.Fatal(err)
	}
	if session.Draft != "note later" {
		t.Fatalf("draft=%q", session.Draft)
	}
	if !session.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("stored updatedAt=%s want=%s", session.UpdatedAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano))
	}
	if session.LastMessageAt == nil || !session.LastMessageAt.Equal(lastMessageAt) {
		if session.LastMessageAt == nil {
			t.Fatal("lastMessageAt was cleared")
		}
		t.Fatalf("stored lastMessageAt=%s want=%s", session.LastMessageAt.Format(time.RFC3339Nano), lastMessageAt.Format(time.RFC3339Nano))
	}
}

func TestConsoleChatPageOmitsAdvancedSettings(t *testing.T) {
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
	if strings.Contains(html, "chat-sidebar-toggle") || strings.Contains(html, "chat-console-link") {
		t.Fatalf("chat page should not render the removed sidebar and console buttons: %s", html)
	}
	if strings.Contains(html, "chat-shell is-sidebar-collapsed") {
		t.Fatalf("chat page should keep the sidebar visible by default: %s", html)
	}
	if strings.Contains(html, `id="chat-advanced"`) || strings.Contains(html, `>高级设置<`) || strings.Contains(html, `>System prompt<`) {
		t.Fatalf("advanced chat settings should be omitted from chat page: %s", html)
	}
	if !strings.Contains(html, `<textarea name="chat_system_prompt" hidden>`) {
		t.Fatalf("chat page should keep a hidden system prompt field: %s", html)
	}
	if strings.Contains(html, "Server sessions") || strings.Contains(html, "Stored on the server and resumable after disconnects") || strings.Contains(html, "Sessions are stored on the server so completed or partial output can be recovered after a disconnect.") {
		t.Fatalf("chat page should omit the removed server-session copy: %s", html)
	}
	if strings.Contains(html, `data-chat-session-title`) || strings.Contains(html, `chat-stage-title`) {
		t.Fatalf("chat page should not render the removed centered stage title: %s", html)
	}
	if strings.Contains(html, `data-chat-primary-label`) {
		t.Fatalf("chat page should render icon-only primary composer action: %s", html)
	}
	if !strings.Contains(html, `data-chat-primary-start`) || !strings.Contains(html, `data-chat-primary-stop`) {
		t.Fatalf("chat page should render start and stop icons for the primary composer action: %s", html)
	}
	if !strings.Contains(html, `https://cdn.jsdmirror.com/npm/lucide@1.16.0/dist/umd/lucide.min.js`) {
		t.Fatalf("chat page should load lucide for chat control icons: %s", html)
	}
	if !strings.Contains(html, `data-lucide="send-horizontal"`) || !strings.Contains(html, `data-lucide="square"`) {
		t.Fatalf("chat page should render lucide placeholders for the primary composer action: %s", html)
	}
}

func TestConsoleRemovedWorkbenchRoutesReturnNotFound(t *testing.T) {
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
	for _, path := range []string{"/console/chat/legacy", "/console/openui/", "/console/chat/bootstrap", "/console/chat/api/stream"} {
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("expected %s to return 404, got %d", path, response.StatusCode)
		}
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
		_, _ = w.Write([]byte(`{"id":"chatcmpl_console","object":"chat.completion","created":1710000000,"model":"openai/gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"Route failover via the weighted provider list."},"finish_reason":"stop"}],"usage":{"prompt_tokens":21,"completion_tokens":9,"total_tokens":30}}`))
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_console", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_console", Enabled: true, Priority: 1, Weight: 100}); err != nil {
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
		"chat_public_name":     {"gpt-5.4"},
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
	if record.UserID != "admin@example.com" || record.ProviderID != "openrouter" || record.ModelAlias != "gpt-5.4" || record.Protocol != domain.ProtocolOpenAI || record.Endpoint != "/console/chat" {
		t.Fatalf("unexpected usage identity: %+v", record)
	}
	if record.InputTokens != 21 || record.OutputTokens != 9 || record.TotalTokens != 30 || record.CostMicroCents != 39 || record.StatusCode != http.StatusOK {
		t.Fatalf("unexpected usage accounting: %+v", record)
	}
}

func TestConsoleChatPageForwardsDeepSeekReasoningEffort(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		payload := string(body)
		if !strings.Contains(payload, `"reasoning_effort":"high"`) {
			t.Fatalf("reasoning effort missing from DeepSeek payload: %s", payload)
		}
		if !strings.Contains(payload, `"thinking":{"type":"enabled"}`) {
			t.Fatalf("thinking enable flag missing from DeepSeek payload: %s", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_deepseek_reasoning","object":"chat.completion","created":1710000000,"model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"先做高强度思考，再返回结论。"},"finish_reason":"stop"}],"usage":{"prompt_tokens":18,"completion_tokens":10,"total_tokens":28}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-deepseek-test", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "deepseek-v4-pro", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
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
		"chat_public_name":      {"deepseek-v4-pro"},
		"chat_reasoning_effort": {"high"},
		"chat_draft":            {"请先思考再回答。"},
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
	if !strings.Contains(string(body), "先做高强度思考，再返回结论。") {
		t.Fatalf("assistant output missing from chat html: %s", body)
	}
}

func TestConsoleChatStreamPersistsCompletedSessionAfterClientDisconnect(t *testing.T) {
	allowFinish := make(chan struct{})
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_resume\",\"choices\":[{\"delta\":{\"content\":\"Route \"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-allowFinish
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"continues after disconnect.\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":5,\"total_tokens\":13}}\n\n"))
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_resume", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_resume", Enabled: true, Priority: 1, Weight: 100}); err != nil {
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
		"chat_client_session_id": {"session-disconnect"},
		"chat_public_name":       {"gpt-5.4"},
		"chat_draft":             {"How would you route failover?"},
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
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	if !strings.Contains(line, `"type":"delta"`) {
		response.Body.Close()
		t.Fatalf("unexpected first stream line: %s", line)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	close(allowFinish)

	deadline := time.Now().Add(3 * time.Second)
	for {
		session, err := store.GetConsoleChatSession(ctx, "admin@example.com", "session-disconnect")
		if err == nil && session.Status == "completed" && strings.Contains(session.MessagesJSON, "Route continues after disconnect.") {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("session not persisted before timeout: %v", err)
			}
			t.Fatalf("session not completed before timeout: %+v", session)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestConsoleChatPageShowsReasoningWithoutFinalAnswer(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_reasoning","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"Inspect route weights before selecting a provider.","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":0,"total_tokens":12}}`))
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_reasoning", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_reasoning", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"gpt-5.4"}, "chat_draft": {"How would you route failover?"}}
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
	if !strings.Contains(html, "Inspect route weights before selecting a provider.") {
		t.Fatalf("reasoning missing from chat html: %s", html)
	}
	if !strings.Contains(html, "模型只返回了思考过程，没有返回最终答复。") {
		t.Fatalf("reasoning-only placeholder missing from chat html: %s", html)
	}
	if !strings.Contains(html, "chat-reasoning") {
		t.Fatalf("reasoning panel missing from chat html: %s", html)
	}
}

func TestConsoleChatPageFailureWritesUsage(t *testing.T) {
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
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"gpt-5.4"}, "chat_draft": {"How would you route failover?"}}
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_stream", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"gpt-5.4"}, "chat_draft": {"How would you route failover?"}}
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
	var doneResult *consoleChatStreamTestResult
	var replaceHTML string
	for _, event := range events {
		if event.Type == "delta" {
			deltaText.WriteString(event.Delta)
		}
		if event.Type == "done" {
			doneResult = event.Result
		}
		if event.Type == "replace" {
			replaceHTML = event.HTML
		}
	}
	if deltaText.String() != "Route failover via weights." {
		t.Fatalf("unexpected streamed delta text: %q", deltaText.String())
	}
	if doneResult == nil || doneResult.Output != "Route failover via weights." || doneResult.FinishReason != "stop" || doneResult.TotalTokens != 12 {
		t.Fatalf("unexpected done event: %+v", doneResult)
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
}

func TestConsoleChatStreamAllowsActiveLongRunningStream(t *testing.T) {
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
		time.Sleep(700 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"failover via weights.\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(700 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-stream", Status: domain.StatusEnabled, TimeoutSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_stream", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"gpt-5.4"}, "chat_draft": {"How would you route failover?"}}
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
	var doneResult *consoleChatStreamTestResult
	var replaceHTML string
	for _, event := range events {
		if event.Type == "delta" {
			deltaText.WriteString(event.Delta)
		}
		if event.Type == "done" {
			doneResult = event.Result
		}
		if event.Type == "replace" {
			replaceHTML = event.HTML
		}
	}
	if deltaText.String() != "Route failover via weights." {
		t.Fatalf("unexpected streamed delta text: %q", deltaText.String())
	}
	if doneResult == nil || doneResult.Output != "Route failover via weights." || doneResult.TotalTokens != 12 {
		t.Fatalf("unexpected done event: %+v", doneResult)
	}
	if !strings.Contains(replaceHTML, "Route failover via weights.") {
		t.Fatalf("stream replacement html missing assistant output: %s", replaceHTML)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || !usage[0].Stream || usage[0].TotalTokens != 12 {
		t.Fatalf("unexpected streamed usage record: %+v", usage)
	}
}

func TestConsoleChatStreamEndpointEmitsReasoningAndPlaceholder(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_reasoning\",\"choices\":[{\"delta\":{\"reasoning_content\":\"Inspect route weights. \"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Check provider health.\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}\n\n"))
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_stream", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"gpt-5.4"}, "chat_draft": {"How would you route failover?"}}
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
	var reasoningText strings.Builder
	var doneResult *consoleChatStreamTestResult
	var replaceHTML string
	for _, event := range events {
		if event.Type == "reasoning" {
			reasoningText.WriteString(event.Reasoning)
		}
		if event.Type == "done" {
			doneResult = event.Result
		}
		if event.Type == "replace" {
			replaceHTML = event.HTML
		}
	}
	if reasoningText.String() != "Inspect route weights. Check provider health." {
		t.Fatalf("unexpected streamed reasoning text: %q", reasoningText.String())
	}
	if doneResult == nil || doneResult.Output != "模型只返回了思考过程，没有返回最终答复。" || doneResult.TotalTokens != 11 {
		t.Fatalf("unexpected reasoning done event: %+v", doneResult)
	}
	if !strings.Contains(replaceHTML, "Inspect route weights. Check provider health.") {
		t.Fatalf("stream replacement html missing reasoning: %s", replaceHTML)
	}
	if !strings.Contains(replaceHTML, "模型只返回了思考过程，没有返回最终答复。") {
		t.Fatalf("stream replacement html missing reasoning-only placeholder: %s", replaceHTML)
	}
	usage, err := store.ListUsage(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].TotalTokens != 11 {
		t.Fatalf("unexpected streamed reasoning usage records: %+v", usage)
	}
}

func TestConsoleChatStreamEndpointAcceptsMultipartFormData(t *testing.T) {
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_stream", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("chat_client_session_id", "session-multipart"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("chat_public_name", "gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("chat_draft", "How would you route failover?"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("chat_history_json", "[]"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/x-ndjson")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("chat stream status=%d body=%s", response.StatusCode, payload)
	}
	streamBody, _ := io.ReadAll(response.Body)
	events := decodeConsoleChatStreamEvents(t, streamBody)
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
	if !strings.Contains(replaceHTML, "session-multipart") {
		t.Fatalf("stream replacement html missing client session id: %s", replaceHTML)
	}
}

func TestConsoleChatStreamEndpointEmitsHeartbeatDuringIdleGap(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"delta\":{\"content\":\"Route \"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(10500 * time.Millisecond)
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
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_stream", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"gpt-5.4"}, "chat_draft": {"How would you route failover?"}}
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
	heartbeatCount := 0
	for _, event := range events {
		if event.Type == "heartbeat" {
			heartbeatCount++
		}
	}
	if heartbeatCount == 0 {
		t.Fatalf("expected at least one heartbeat event, got %+v", events)
	}
}

func TestConsoleChatStreamEndpointEmitsErrorEventWithPartialOutput(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"delta\":{\"content\":\"Partial answer.\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(1200 * time.Millisecond)
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-stream", Status: domain.StatusEnabled, TimeoutSeconds: 30, StreamIdleTimeoutSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_gpt54_stream", ModelAlias: "gpt-5.4", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_gpt54_stream", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	cfg := app.Config{HTTPAddr: ":0", SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	server := httptest.NewServer(app.NewServer(cfg, store).Handler())
	defer server.Close()

	client, err := loggedInConsoleClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"gpt-5.4"}, "chat_draft": {"How would you route failover?"}}
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
	var errorEvent *consoleChatStreamTestEvent
	var replaceHTML string
	for index := range events {
		if events[index].Type == "error" {
			errorEvent = &events[index]
		}
		if events[index].Type == "replace" {
			replaceHTML = events[index].HTML
		}
	}
	if errorEvent == nil || !strings.Contains(errorEvent.Error, "对话失败：") {
		t.Fatalf("expected error event, got %+v", events)
	}
	if !strings.Contains(errorEvent.Error, "completion marker") {
		t.Fatalf("expected explicit upstream completion marker reason, got %q", errorEvent.Error)
	}
	if errorEvent.Message == nil || errorEvent.Message.Content != "Partial answer." {
		t.Fatalf("error event should preserve partial output: %+v", errorEvent)
	}
	if !strings.Contains(replaceHTML, "Partial answer.") {
		t.Fatalf("stream replacement html missing partial assistant output: %s", replaceHTML)
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, "console chat stream interrupted") || !strings.Contains(logOutput, "reason_code=stream_unexpected_eof") {
		t.Fatalf("expected interrupted stream log with explicit reason code, got %s", logOutput)
	}
}

func TestConsoleChatPageShowsAllCompatibleEnabledRoutesWithoutAPIKeyFilter(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.example/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-openrouter", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://deepseek.example/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-deepseek", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "anthropic-main", Name: "Anthropic", BaseURL: "https://anthropic.example/v1", Protocol: domain.ProtocolAnthropic, MasterKey: "sk-ant-stream", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []domain.ModelRoute{
		{PublicName: "claude-sonnet", ProviderID: "anthropic-main", UpstreamModel: "claude-sonnet-4-5", Protocol: domain.ProtocolAnthropic, AllowedProtocols: []string{domain.ProtocolAnthropic}, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "claude-opus-4.7", ProviderID: "anthropic-main", UpstreamModel: "claude-opus-4.7", Protocol: domain.ProtocolAnthropic, AllowedProtocols: []string{domain.ProtocolAnthropic}, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "claude-sonnet-4.6", ProviderID: "anthropic-main", UpstreamModel: "claude-sonnet-4.6", Protocol: domain.ProtocolAnthropic, AllowedProtocols: []string{domain.ProtocolAnthropic}, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "deepseek-v4-flash", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-flash", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 2, Weight: 100},
		{PublicName: "deepseek-v4-pro", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "gpt-5.5", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.5", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "gpt-5.5-pro", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.5-pro", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "gemini-3-flash", ProviderID: "openrouter", UpstreamModel: "google/gemini-3-flash-preview", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "gemini-3.1-pro-preview", ProviderID: "openrouter", UpstreamModel: "google/gemini-3.1-pro-preview", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "chatgpt-image-2", ProviderID: "openrouter", UpstreamModel: "chatgpt-image-2", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
		{PublicName: "gpt-4.1-mini", ProviderID: "openrouter", UpstreamModel: "openai/gpt-4.1-mini", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100},
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

	pageResponse, err := client.Get(server.URL + "/console/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	for _, expected := range []string{"claude-sonnet", "claude-opus-4.7", "claude-sonnet-4.6", "deepseek-v4-flash", "deepseek-v4-pro", "gpt-5.4", "gpt-5.5", "gpt-5.5-pro", "gemini-3-flash", "gemini-3.1-pro-preview", "chatgpt-image-2", "gpt-4.1-mini"} {
		if !strings.Contains(pageHTML, expected) {
			t.Fatalf("expected compatible route %q in chat page: %s", expected, pageHTML)
		}
	}
	if !strings.Contains(pageHTML, `value="chatgpt-image-2" checked`) {
		t.Fatalf("expected chatgpt-image-2 to be the default selected route: %s", pageHTML)
	}
	if strings.Contains(pageHTML, "帮我总结当前 public model 对应的上游路由和潜在故障点") {
		t.Fatalf("starter prompt cards should be removed from chat page: %s", pageHTML)
	}
	if strings.Contains(pageHTML, `aria-controls="chat-advanced" onclick="const panel=document.getElementById('chat-advanced');if(panel){panel.open=true;panel.scrollIntoView({behavior:'smooth',block:'nearest'});}"`) {
		t.Fatalf("tools shortcut should be hidden when no tools are available: %s", pageHTML)
	}
	if strings.Contains(pageHTML, `id="chat-advanced"`) || strings.Contains(pageHTML, `>高级设置<`) || strings.Contains(pageHTML, `>System prompt<`) {
		t.Fatalf("advanced chat settings should be omitted from chat page: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, `<textarea name="chat_system_prompt" hidden>`) {
		t.Fatalf("chat page should keep a hidden system prompt field: %s", pageHTML)
	}
	if strings.Contains(pageHTML, `data-chat-action="pick-attachments"`) {
		t.Fatalf("attachment quick action should stay hidden when attachment upload is disabled: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, `data-chat-attachment-upload-enabled="false"`) {
		t.Fatalf("chat page should surface disabled attachment upload state: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, `data-chat-reasoning-efforts="high,max"`) {
		t.Fatalf("expected DeepSeek reasoning effort metadata in chat page: %s", pageHTML)
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
	if !strings.Contains(html, `name="stream_idle_timeout_seconds" type="number" min="1" value="300"`) {
		t.Fatalf("proxy edit form did not load stream idle timeout: %s", html)
	}

	form := url.Values{
		"id":                          {"edge-socks"},
		"name":                        {"Edge SOCKS Updated"},
		"type":                        {"socks5"},
		"endpoint":                    {"127.0.0.1:20808"},
		"region":                      {"jp"},
		"timeout_seconds":             {"80"},
		"stream_idle_timeout_seconds": {"420"},
		"health_check_url":            {"https://probe.example.com/healthz"},
		"status":                      {domain.StatusEnabled},
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
	if profile.Name != "Edge SOCKS Updated" || profile.Endpoint != "socks5://127.0.0.1:20808" || profile.Region != "jp" || profile.TimeoutSeconds != 80 || profile.StreamIdleTimeoutSeconds != 420 || profile.HealthCheckURL != "https://probe.example.com/healthz" || profile.Status != domain.StatusEnabled {
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
	if !strings.Contains(pageHTML, "内置直连，不可编辑") {
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
	if !strings.Contains(editHTML, "内置 direct Profile 不可编辑") {
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
	if !strings.Contains(string(updateBody), "内置 direct Profile 不可编辑") {
		t.Fatalf("unexpected direct update error: %s", updateBody)
	}

	profile, err := store.GetProxyProfile(ctx, domain.ProxyTypeDirect)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != domain.ProxyTypeDirect || profile.Status != domain.StatusEnabled || profile.TimeoutSeconds != 60 || profile.StreamIdleTimeoutSeconds != 300 {
		t.Fatalf("direct proxy should remain unchanged: %+v", profile)
	}
}

func TestConsoleWorkersPageCreatesSSHKeyAndWorker(t *testing.T) {
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

	pageResponse, err := client.Get(server.URL + "/console/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	if pageResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pageResponse.Body)
		t.Fatalf("workers page status=%d body=%s", pageResponse.StatusCode, body)
	}
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, `action="/console/workers/ssh-keys"`) || !strings.Contains(pageHTML, `action="/console/workers"`) {
		t.Fatalf("workers page missing forms: %s", pageHTML)
	}

	keyForm := url.Values{
		"id":          {"ssh-key-1"},
		"name":        {"Tokyo bootstrap key"},
		"private_key": {mustGenerateConsolePrivateKeyPEM(t)},
		"comment":     {"bootstrap"},
	}
	keyRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/workers/ssh-keys", strings.NewReader(keyForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	keyRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	keyResponse, err := client.Do(keyRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer keyResponse.Body.Close()
	if keyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(keyResponse.Body)
		t.Fatalf("create ssh key status=%d body=%s", keyResponse.StatusCode, body)
	}
	sshKey, err := store.GetWorkerSSHKey(ctx, "ssh-key-1")
	if err != nil {
		t.Fatal(err)
	}
	if sshKey.Username != "" || sshKey.Fingerprint == "" {
		t.Fatalf("unexpected ssh key: %+v", sshKey)
	}

	workerForm := url.Values{
		"id":                      {"worker-1"},
		"name":                    {"Tokyo builder"},
		"expected_ubuntu_version": {domain.DefaultWorkerExpectedUbuntuVersion},
		"ssh_host":                {"10.0.0.5"},
		"ssh_port":                {"22"},
		"ssh_username":            {"ubuntu"},
		"ssh_key_id":              {sshKey.ID},
		"install_proxy_id":        {domain.ProxyTypeDirect},
		"labels":                  {"gpu,asia"},
		"data_root":               {domain.DefaultWorkerDataRoot},
		"data_disks":              {"/dev/vdb /srv/aiyolo"},
	}
	workerRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/workers", strings.NewReader(workerForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	workerRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	workerResponse, err := client.Do(workerRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer workerResponse.Body.Close()
	if workerResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(workerResponse.Body)
		t.Fatalf("create worker status=%d body=%s", workerResponse.StatusCode, body)
	}
	worker, err := store.GetWorkerServer(ctx, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if worker.SSHHost != "10.0.0.5" || worker.SSHKeyID != sshKey.ID || worker.InstallProxyID != domain.ProxyTypeDirect {
		t.Fatalf("unexpected worker: %+v", worker)
	}
	disks, err := store.ListWorkerDataDisks(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].DevicePath != "/dev/vdb" || disks[0].MountPath != "/srv/aiyolo" {
		t.Fatalf("unexpected worker disks: %+v", disks)
	}

	finalPageResponse, err := client.Get(server.URL + "/console/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer finalPageResponse.Body.Close()
	finalBody, _ := io.ReadAll(finalPageResponse.Body)
	finalHTML := string(finalBody)
	if !strings.Contains(finalHTML, "Tokyo builder") || !strings.Contains(finalHTML, sshKey.ID) || !strings.Contains(finalHTML, "/srv/aiyolo") {
		t.Fatalf("workers page missing saved resources: %s", finalHTML)
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

func mustGenerateConsolePrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}

type consoleChatStreamTestEvent struct {
	Type      string                        `json:"type"`
	Delta     string                        `json:"delta"`
	Reasoning string                        `json:"reasoning"`
	HTML      string                        `json:"html"`
	Error     string                        `json:"error"`
	Message   *consoleChatStreamTestMessage `json:"message"`
	Result    *consoleChatStreamTestResult  `json:"result"`
}

type consoleChatStreamTestMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning"`
}

type consoleChatStreamTestResult struct {
	PublicName   string `json:"publicName"`
	Output       string `json:"output"`
	FinishReason string `json:"finishReason"`
	TotalTokens  int    `json:"totalTokens"`
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
