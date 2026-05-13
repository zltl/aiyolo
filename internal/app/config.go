package app

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	HTTPAddr       string
	DatabaseURL    string
	AutoMigrate    bool
	SeedFromEnv    bool
	SecretKey      string
	AdminEmail     string
	AdminPassword  string
	SeedAPIKey     string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	OpenRouterKey  string
	DefaultBaseURL string
	DefaultModel   string
}

func LoadConfig(v *viper.Viper) (Config, error) {
	cfg := Config{
		HTTPAddr:       firstNonEmpty(v.GetString("app.http_addr"), ":8080"),
		DatabaseURL:    firstNonEmpty(v.GetString("database.url"), databaseURLFromViper(v)),
		AutoMigrate:    v.GetBool("app.auto_migrate"),
		SeedFromEnv:    v.GetBool("app.seed_from_env"),
		SecretKey:      firstNonEmpty(v.GetString("auth.secret_key"), "aiyolo-development-secret-change-me"),
		AdminEmail:     firstNonEmpty(v.GetString("auth.admin_email"), "admin@example.com"),
		AdminPassword:  firstNonEmpty(v.GetString("auth.admin_password"), "admin123456"),
		SeedAPIKey:     v.GetString("app.seed_api_key"),
		ReadTimeout:    durationOr(v.GetString("app.read_timeout"), 30*time.Second),
		WriteTimeout:   durationOr(v.GetString("app.write_timeout"), 0),
		IdleTimeout:    durationOr(v.GetString("app.idle_timeout"), 120*time.Second),
		OpenRouterKey:  v.GetString("providers.openrouter.api_key"),
		DefaultBaseURL: firstNonEmpty(v.GetString("providers.openrouter.default_base_url"), "https://openrouter.ai/api/v1"),
		DefaultModel:   firstNonEmpty(v.GetString("providers.openrouter.default_model"), "openrouter/auto"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("database.url or AIYOLO_DATABASE_URL is required")
	}
	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func durationOr(value string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return duration
}

func databaseURLFromViper(v *viper.Viper) string {
	username := strings.TrimSpace(v.GetString("database.username"))
	password := strings.TrimSpace(v.GetString("database.password"))
	if username == "" || password == "" {
		return ""
	}
	host := strings.TrimSpace(v.GetString("database.host_external"))
	if v.IsSet("database.prefer_external") && !v.GetBool("database.prefer_external") {
		host = strings.TrimSpace(v.GetString("database.host_internal"))
	}
	if host == "" {
		host = firstNonEmpty(v.GetString("database.host_external"), v.GetString("database.host_internal"))
	}
	if host == "" {
		return ""
	}
	query := url.Values{}
	query.Set("sslmode", firstNonEmpty(v.GetString("database.sslmode"), "disable"))
	if schema := strings.TrimSpace(v.GetString("database.schema")); schema != "" {
		query.Set("aiyolo_schema", schema)
	}
	return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?%s", url.QueryEscape(username), url.QueryEscape(password), host, firstNonEmpty(v.GetString("database.name"), "bbflow"), query.Encode())
}
