package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigFromYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "aiyolo.private.yaml")
	if err := os.WriteFile(configPath, []byte(`app:
  http_addr: 127.0.0.1:19090
  auto_migrate: true
  seed_from_env: false
  seed_api_key: yaml-seed-key
  read_timeout: 12s
  write_timeout: 3s
  idle_timeout: 44s

auth:
  secret_key: yaml-secret
  admin_email: yaml-admin@example.com
  admin_password: yaml-password

database:
  host_external: db.example.com
  username: yaml-user
  password: yaml-password
  name: yaml-db
  schema: yaml_schema
  sslmode: disable

providers:
  openrouter:
    api_key: yaml-openrouter-key
    default_base_url: https://example.com/v1
    default_model: yaml/model
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIYOLO_CONFIG_FILE", configPath)
	v, _, err := NewViper(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(v)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:19090" {
		t.Fatalf("HTTPAddr=%q", cfg.HTTPAddr)
	}
	if cfg.DatabaseURL != "postgres://yaml-user:yaml-password@db.example.com:5432/yaml-db?aiyolo_schema=yaml_schema&sslmode=disable" {
		t.Fatalf("DatabaseURL=%q", cfg.DatabaseURL)
	}
	if cfg.OpenRouterKey != "yaml-openrouter-key" {
		t.Fatalf("OpenRouterKey=%q", cfg.OpenRouterKey)
	}
	if cfg.AdminEmail != "yaml-admin@example.com" || cfg.AdminPassword != "yaml-password" {
		t.Fatalf("unexpected admin config: %+v", cfg)
	}
	if cfg.ReadTimeout != 12*time.Second || cfg.WriteTimeout != 3*time.Second || cfg.IdleTimeout != 44*time.Second {
		t.Fatalf("unexpected timeouts: read=%s write=%s idle=%s", cfg.ReadTimeout, cfg.WriteTimeout, cfg.IdleTimeout)
	}
	if cfg.SeedFromEnv {
		t.Fatal("SeedFromEnv should be false from yaml")
	}
	if cfg.SeedAPIKey != "yaml-seed-key" {
		t.Fatalf("SeedAPIKey=%q", cfg.SeedAPIKey)
	}

	t.Setenv("AIYOLO_HTTP_ADDR", ":20000")
	v, _, err = NewViper(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(v)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":20000" {
		t.Fatalf("env override failed: HTTPAddr=%q", cfg.HTTPAddr)
	}
}
