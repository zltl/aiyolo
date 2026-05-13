package console_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/app"
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
