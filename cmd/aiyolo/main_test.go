package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLauncherConfigFromEnvPath(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	payload := []byte(`{"api_base_url":"https://gateway.example.com/v1","api_key":"aiyolo_live_1234567890","default_model":"gpt-5.5","allowed_models":["gpt-5.4","gpt-5.5","gpt-5.5-pro"],"codex_command":"codex"}`)
	if err := os.WriteFile(configPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(launcherConfigEnv, configPath)

	cfg, resolvedPath, err := loadLauncherConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedPath != configPath {
		t.Fatalf("resolvedPath=%q", resolvedPath)
	}
	if cfg.APIBaseURL != "https://gateway.example.com/v1" || cfg.DefaultModel != "gpt-5.5" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestRunDoctorUsesModelsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer aiyolo_live_1234567890" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "gpt-5.5"}, {"id": "gpt-5.5-pro"}}})
	}))
	defer server.Close()

	result, err := runDoctor(context.Background(), launcherConfig{APIBaseURL: server.URL + "/v1", APIKey: "aiyolo_live_1234567890"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelCount != 2 {
		t.Fatalf("modelCount=%d", result.ModelCount)
	}
}

func TestCodexCommandEnvInjectsCredentials(t *testing.T) {
	env := codexCommandEnv([]string{"PATH=/tmp/bin", "OPENAI_BASE_URL=https://old.example.com/v1"}, launcherConfig{APIBaseURL: "https://gateway.example.com/v1", APIKey: "aiyolo_live_1234567890", DefaultModel: "gpt-5.5"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "OPENAI_API_KEY=aiyolo_live_1234567890") {
		t.Fatalf("missing api key env: %s", joined)
	}
	if !strings.Contains(joined, "CODEX_API_KEY=aiyolo_live_1234567890") {
		t.Fatalf("missing codex api key env: %s", joined)
	}
	if !strings.Contains(joined, "OPENAI_BASE_URL=https://gateway.example.com/v1") {
		t.Fatalf("missing api base env: %s", joined)
	}
	if !strings.Contains(joined, "AIYOLO_DEFAULT_MODEL=gpt-5.5") {
		t.Fatalf("missing default model env: %s", joined)
	}
}

func TestRedactAPIKey(t *testing.T) {
	redacted := redactAPIKey("aiyolo_live_1234567890")
	if redacted == "aiyolo_live_1234567890" {
		t.Fatalf("api key should be redacted")
	}
	if !strings.HasPrefix(redacted, "aiyolo_") || !strings.HasSuffix(redacted, "7890") {
		t.Fatalf("unexpected redaction: %s", redacted)
	}
}
