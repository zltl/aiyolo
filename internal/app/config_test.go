package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigFromYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "aiyolo.private.yaml")
	configYAML := strings.Join([]string{
		"app:",
		"  http_addr: 127.0.0.1:19090",
		"  auto_migrate: true",
		"  seed_from_env: false",
		"  seed_api_key: yaml-seed-key",
		"  read_timeout: 12s",
		"  write_timeout: 3s",
		"  idle_timeout: 44s",
		"",
		"auth:",
		"  secret_key: yaml-secret",
		"  admin_email: yaml-admin@example.com",
		"  admin_password: yaml-password",
		"",
		"artifacts:",
		"  public_base_url: https://aiyolo.quant67.com",
		"  public_via_proxy: true",
		"  proxy_base_path: /artifacts",
		"",
		"database:",
		"  host_external: db.example.com",
		"  username: yaml-user",
		"  password: yaml-password",
		"  name: yaml-db",
		"  schema: yaml_schema",
		"  sslmode: disable",
	}, "\n") + "\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
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
	if !cfg.Artifacts.PublicViaProxy || cfg.Artifacts.PublicBaseURL != "https://aiyolo.quant67.com" {
		t.Fatalf("unexpected artifacts config: %+v", cfg.Artifacts)
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
