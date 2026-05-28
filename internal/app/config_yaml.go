package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func NewViper(configFile string) (*viper.Viper, string, error) {
	v := viper.New()
	v.SetEnvPrefix("AIYOLO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	bindEnvKeys(v)
	setDefaults(v)
	resolved, err := resolveConfigFile(configFile)
	if err != nil {
		return nil, "", err
	}
	if resolved != "" {
		v.SetConfigFile(resolved)
		if err := v.ReadInConfig(); err != nil {
			return nil, "", fmt.Errorf("read config %s: %w", resolved, err)
		}
	}
	return v, resolved, nil
}

func bindEnvKeys(v *viper.Viper) {
	bindings := map[string]string{
		"app.http_addr":                         "AIYOLO_HTTP_ADDR",
		"app.auto_migrate":                      "AIYOLO_AUTO_MIGRATE",
		"app.seed_from_env":                     "AIYOLO_SEED_FROM_ENV",
		"app.seed_api_key":                      "AIYOLO_SEED_API_KEY",
		"artifacts.public_base_url":             "AIYOLO_ARTIFACTS_PUBLIC_BASE_URL",
		"artifacts.public_via_proxy":            "AIYOLO_ARTIFACTS_PUBLIC_VIA_PROXY",
		"artifacts.proxy_base_path":             "AIYOLO_ARTIFACTS_PROXY_BASE_PATH",
		"artifacts.s3.endpoint":                 "AIYOLO_ARTIFACTS_S3_ENDPOINT",
		"artifacts.s3.internal_endpoint":        "AIYOLO_ARTIFACTS_S3_INTERNAL_ENDPOINT",
		"artifacts.s3.region":                   "AIYOLO_ARTIFACTS_S3_REGION",
		"artifacts.s3.bucket":                   "AIYOLO_ARTIFACTS_S3_BUCKET",
		"artifacts.s3.prefix":                   "AIYOLO_ARTIFACTS_S3_PREFIX",
		"artifacts.s3.access_key_id":            "AIYOLO_ARTIFACTS_S3_ACCESS_KEY_ID",
		"artifacts.s3.access_key_secret":        "AIYOLO_ARTIFACTS_S3_ACCESS_KEY_SECRET",
		"artifacts.s3.bucket_domain":            "AIYOLO_ARTIFACTS_S3_BUCKET_DOMAIN",
		"artifacts.s3.cname_domain":             "AIYOLO_ARTIFACTS_S3_CNAME_DOMAIN",
		"artifacts.s3.use_internal":             "AIYOLO_ARTIFACTS_S3_USE_INTERNAL",
		"chat.attachments.public_base_url":      "AIYOLO_CHAT_ATTACHMENTS_PUBLIC_BASE_URL",
		"chat.attachments.public_via_proxy":     "AIYOLO_CHAT_ATTACHMENTS_PUBLIC_VIA_PROXY",
		"chat.attachments.proxy_base_path":      "AIYOLO_CHAT_ATTACHMENTS_PROXY_BASE_PATH",
		"chat.attachments.s3.endpoint":          "AIYOLO_CHAT_ATTACHMENTS_S3_ENDPOINT",
		"chat.attachments.s3.internal_endpoint": "AIYOLO_CHAT_ATTACHMENTS_S3_INTERNAL_ENDPOINT",
		"chat.attachments.s3.region":            "AIYOLO_CHAT_ATTACHMENTS_S3_REGION",
		"chat.attachments.s3.bucket":            "AIYOLO_CHAT_ATTACHMENTS_S3_BUCKET",
		"chat.attachments.s3.prefix":            "AIYOLO_CHAT_ATTACHMENTS_S3_PREFIX",
		"chat.attachments.s3.access_key_id":     "AIYOLO_CHAT_ATTACHMENTS_S3_ACCESS_KEY_ID",
		"chat.attachments.s3.access_key_secret": "AIYOLO_CHAT_ATTACHMENTS_S3_ACCESS_KEY_SECRET",
		"chat.attachments.s3.bucket_domain":     "AIYOLO_CHAT_ATTACHMENTS_S3_BUCKET_DOMAIN",
		"chat.attachments.s3.cname_domain":      "AIYOLO_CHAT_ATTACHMENTS_S3_CNAME_DOMAIN",
		"chat.attachments.s3.use_internal":      "AIYOLO_CHAT_ATTACHMENTS_S3_USE_INTERNAL",
		"codex.public_base_url":                 "AIYOLO_CODEX_PUBLIC_BASE_URL",
		"codex.install_token_ttl":               "AIYOLO_CODEX_INSTALL_TOKEN_TTL",
		"codex.windows_wrapper_url":             "AIYOLO_CODEX_WINDOWS_WRAPPER_URL",
		"codex.windows_wrapper_sha256":          "AIYOLO_CODEX_WINDOWS_WRAPPER_SHA256",
		"app.read_timeout":                      "AIYOLO_READ_TIMEOUT",
		"app.write_timeout":                     "AIYOLO_WRITE_TIMEOUT",
		"app.idle_timeout":                      "AIYOLO_IDLE_TIMEOUT",
		"auth.secret_key":                       "AIYOLO_SECRET_KEY",
		"auth.admin_email":                      "AIYOLO_ADMIN_EMAIL",
		"auth.admin_password":                   "AIYOLO_ADMIN_PASSWORD",
		"database.url":                          "AIYOLO_DATABASE_URL",
		"database.host_internal":                "AIYOLO_DATABASE_HOST_INTERNAL",
		"database.host_external":                "AIYOLO_DATABASE_HOST_EXTERNAL",
		"database.username":                     "AIYOLO_DATABASE_USERNAME",
		"database.password":                     "AIYOLO_DATABASE_PASSWORD",
		"database.name":                         "AIYOLO_DATABASE_NAME",
		"database.schema":                       "AIYOLO_DATABASE_SCHEMA",
		"database.sslmode":                      "AIYOLO_DATABASE_SSLMODE",
		"database.prefer_external":              "AIYOLO_DATABASE_PREFER_EXTERNAL",
	}
	for key, envVar := range bindings {
		_ = v.BindEnv(key, envVar)
	}
}

func BindStringFlags(v *viper.Viper, flags *pflag.FlagSet) error {
	bindings := map[string]string{
		"config":                       "config",
		"http-addr":                    "app.http_addr",
		"database-url":                 "database.url",
		"seed-api-key":                 "app.seed_api_key",
		"codex-public-base-url":        "codex.public_base_url",
		"codex-install-token-ttl":      "codex.install_token_ttl",
		"codex-windows-wrapper-url":    "codex.windows_wrapper_url",
		"codex-windows-wrapper-sha256": "codex.windows_wrapper_sha256",
		"secret-key":                   "auth.secret_key",
		"admin-email":                  "auth.admin_email",
		"admin-password":               "auth.admin_password",
		"read-timeout":                 "app.read_timeout",
		"write-timeout":                "app.write_timeout",
		"idle-timeout":                 "app.idle_timeout",
	}
	for flagName, key := range bindings {
		if err := v.BindPFlag(key, flags.Lookup(flagName)); err != nil {
			return err
		}
	}
	return nil
}

func resolveConfigFile(configFile string) (string, error) {
	for _, path := range configCandidatePaths(configFile) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		resolved := filepath.Clean(path)
		_, err := os.Stat(resolved)
		if err == nil {
			return resolved, nil
		}
		if os.IsNotExist(err) {
			if path == configFile || (configFile == "" && os.Getenv("AIYOLO_CONFIG_FILE") == path) {
				return "", fmt.Errorf("config file does not exist: %s", path)
			}
			continue
		}
		return "", err
	}
	return "", nil
}

func configCandidatePaths(configFile string) []string {
	paths := []string{}
	if strings.TrimSpace(configFile) != "" {
		paths = append(paths, configFile)
		return paths
	}
	if envPath := strings.TrimSpace(os.Getenv("AIYOLO_CONFIG_FILE")); envPath != "" {
		paths = append(paths, envPath)
	}
	paths = append(paths, "aiyolo.private.yaml", "aiyolo.yaml")
	return paths
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.http_addr", ":8080")
	v.SetDefault("app.auto_migrate", true)
	v.SetDefault("app.seed_from_env", true)
	v.SetDefault("app.read_timeout", "30s")
	v.SetDefault("app.write_timeout", "0s")
	v.SetDefault("app.idle_timeout", "120s")
	v.SetDefault("artifacts.proxy_base_path", "/artifacts")
	v.SetDefault("artifacts.public_via_proxy", false)
	v.SetDefault("chat.attachments.proxy_base_path", "/console/chat/attachments/files")
	v.SetDefault("chat.attachments.public_via_proxy", false)
	v.SetDefault("codex.install_token_ttl", "15m")
	v.SetDefault("codex.windows_wrapper_url", "/console/codex/artifacts/aiyolo.exe")
	v.SetDefault("database.name", "bbflow")
	v.SetDefault("database.schema", "aiyolo")
	v.SetDefault("database.sslmode", "disable")
	v.SetDefault("database.prefer_external", true)
}

func AddConfigFlags(flags *pflag.FlagSet) {
	flags.String("config", "", "Path to YAML config file")
	flags.String("http-addr", "", "HTTP listen address")
	flags.String("database-url", "", "PostgreSQL connection URL")
	flags.Bool("auto-migrate", false, "Run database migrations before serving")
	flags.Bool("seed-from-env", false, "Seed built-in defaults and API key settings before serving")
	flags.String("seed-api-key", "", "Seed a local API key at startup")
	flags.String("codex-public-base-url", "", "Browser-facing AIYolo base URL used in Codex install scripts")
	flags.String("codex-install-token-ttl", "", "Codex install link lifetime, e.g. 15m")
	flags.String("codex-windows-wrapper-url", "", "Windows aiyolo.exe wrapper download URL or console path")
	flags.String("codex-windows-wrapper-sha256", "", "Optional SHA-256 checksum for the Windows aiyolo.exe wrapper")
	flags.String("secret-key", "", "Application secret key")
	flags.String("admin-email", "", "Console admin email")
	flags.String("admin-password", "", "Console admin password")
	flags.String("read-timeout", "", "Server read timeout, e.g. 30s")
	flags.String("write-timeout", "", "Server write timeout, e.g. 30s")
	flags.String("idle-timeout", "", "Server idle timeout, e.g. 120s")
}

func ApplyFlagOverrides(cmd *cobra.Command, cfg *Config) error {
	if cmd.Flags().Changed("auto-migrate") {
		value, err := cmd.Flags().GetBool("auto-migrate")
		if err != nil {
			return err
		}
		cfg.AutoMigrate = value
	}
	if cmd.Flags().Changed("seed-from-env") {
		value, err := cmd.Flags().GetBool("seed-from-env")
		if err != nil {
			return err
		}
		cfg.SeedFromEnv = value
	}
	return nil
}
