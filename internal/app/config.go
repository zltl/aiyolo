package app

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
	"github.com/zltl/aiyolo/internal/artifacts"
)

type Config struct {
	HTTPAddr                  string
	DatabaseURL               string
	AutoMigrate               bool
	SeedFromEnv               bool
	SecretKey                 string
	AdminEmail                string
	AdminPassword             string
	SeedAPIKey                string
	Artifacts                 artifacts.Config
	ChatAttachments           artifacts.Config
	CodexPublicBaseURL        string
	CodexInstallTokenTTL      time.Duration
	CodexWindowsWrapperURL    string
	CodexWindowsWrapperSHA256 string
	ReadTimeout               time.Duration
	WriteTimeout              time.Duration
	IdleTimeout               time.Duration
}

func LoadConfig(v *viper.Viper) (Config, error) {
	cfg := Config{
		HTTPAddr:                  firstNonEmpty(v.GetString("app.http_addr"), ":8080"),
		DatabaseURL:               firstNonEmpty(v.GetString("database.url"), databaseURLFromViper(v)),
		AutoMigrate:               v.GetBool("app.auto_migrate"),
		SeedFromEnv:               v.GetBool("app.seed_from_env"),
		SecretKey:                 firstNonEmpty(v.GetString("auth.secret_key"), "aiyolo-development-secret-change-me"),
		AdminEmail:                firstNonEmpty(v.GetString("auth.admin_email"), "admin@example.com"),
		AdminPassword:             firstNonEmpty(v.GetString("auth.admin_password"), "admin123456"),
		SeedAPIKey:                v.GetString("app.seed_api_key"),
		Artifacts:                 loadArtifactsConfig(v, "artifacts"),
		ChatAttachments:           loadArtifactsConfig(v, "chat.attachments"),
		CodexPublicBaseURL:        strings.TrimSpace(v.GetString("codex.public_base_url")),
		CodexInstallTokenTTL:      durationOr(v.GetString("codex.install_token_ttl"), 15*time.Minute),
		CodexWindowsWrapperURL:    strings.TrimSpace(v.GetString("codex.windows_wrapper_url")),
		CodexWindowsWrapperSHA256: strings.TrimSpace(v.GetString("codex.windows_wrapper_sha256")),
		ReadTimeout:               durationOr(v.GetString("app.read_timeout"), 30*time.Second),
		WriteTimeout:              durationOr(v.GetString("app.write_timeout"), 0),
		IdleTimeout:               durationOr(v.GetString("app.idle_timeout"), 120*time.Second),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("database.url or AIYOLO_DATABASE_URL is required")
	}
	if err := validateArtifactsConfig("artifacts", cfg.Artifacts); err != nil {
		return cfg, err
	}
	if err := validateArtifactsConfig("chat.attachments", cfg.ChatAttachments); err != nil {
		return cfg, err
	}
	if err := validateAbsoluteHTTPURL(cfg.CodexPublicBaseURL); err != nil {
		return cfg, fmt.Errorf("codex.public_base_url: %w", err)
	}
	if err := validateBrowserEntryURL(cfg.CodexWindowsWrapperURL); err != nil {
		return cfg, fmt.Errorf("codex.windows_wrapper_url: %w", err)
	}
	return cfg, nil
}

func loadArtifactsConfig(v *viper.Viper, prefix string) artifacts.Config {
	return artifacts.Config{
		PublicBaseURL:  strings.TrimSpace(v.GetString(prefix + ".public_base_url")),
		ProxyBasePath:  strings.TrimSpace(v.GetString(prefix + ".proxy_base_path")),
		PublicViaProxy: v.GetBool(prefix + ".public_via_proxy"),
		S3: artifacts.S3Config{
			Endpoint:         strings.TrimSpace(v.GetString(prefix + ".s3.endpoint")),
			InternalEndpoint: strings.TrimSpace(v.GetString(prefix + ".s3.internal_endpoint")),
			Region:           strings.TrimSpace(v.GetString(prefix + ".s3.region")),
			Bucket:           strings.TrimSpace(v.GetString(prefix + ".s3.bucket")),
			Prefix:           strings.TrimSpace(v.GetString(prefix + ".s3.prefix")),
			AccessKeyID:      strings.TrimSpace(v.GetString(prefix + ".s3.access_key_id")),
			AccessKeySecret:  strings.TrimSpace(v.GetString(prefix + ".s3.access_key_secret")),
			BucketDomain:     strings.TrimSpace(v.GetString(prefix + ".s3.bucket_domain")),
			CNAMEDomain:      strings.TrimSpace(v.GetString(prefix + ".s3.cname_domain")),
			UseInternal:      v.GetBool(prefix + ".s3.use_internal"),
		},
	}
}

func validateArtifactsConfig(prefix string, cfg artifacts.Config) error {
	if err := validateBrowserEntryURL(cfg.PublicBaseURL); err != nil {
		return fmt.Errorf("%s.public_base_url: %w", prefix, err)
	}
	if cfg.PublicViaProxy && strings.TrimSpace(cfg.PublicBaseURL) == "" {
		return fmt.Errorf("%s.public_base_url is required when %s.public_via_proxy is true", prefix, prefix)
	}
	if err := validateAbsoluteHTTPURL(cfg.S3.Endpoint); err != nil {
		return fmt.Errorf("%s.s3.endpoint: %w", prefix, err)
	}
	if err := validateAbsoluteHTTPURL(cfg.S3.InternalEndpoint); err != nil {
		return fmt.Errorf("%s.s3.internal_endpoint: %w", prefix, err)
	}
	if err := validateBrowserEntryURL(cfg.S3.BucketDomain); err != nil {
		return fmt.Errorf("%s.s3.bucket_domain: %w", prefix, err)
	}
	if err := validateBrowserEntryURL(cfg.S3.CNAMEDomain); err != nil {
		return fmt.Errorf("%s.s3.cname_domain: %w", prefix, err)
	}
	return nil
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

func validateBrowserEntryURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "/") {
		return nil
	}
	return validateAbsoluteHTTPURL(raw)
}

func validateAbsoluteHTTPURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("host is required")
	}
	return nil
}
