package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aiyolo-gateway",
		Short:        "Run the AIYolo API gateway and console",
		SilenceUsage: true,
		RunE:         runServe,
	}
	AddConfigFlags(cmd.PersistentFlags())
	cmd.AddCommand(newPublishArtifactsCommand())
	cmd.AddCommand(newBuildCloudAgentImageOnWorkerCommand())
	return cmd
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfg, err := loadConfigFromCommand(cmd)
	if err != nil {
		return err
	}
	return Run(cmd.Context(), cfg)
}

func loadConfigFromCommand(cmd *cobra.Command) (Config, error) {
	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return Config{}, err
	}
	v, _, err := NewViper(configFile)
	if err != nil {
		return Config{}, err
	}
	if err := BindStringFlags(v, cmd.Flags()); err != nil {
		return Config{}, err
	}
	cfg, err := LoadConfig(v)
	if err != nil {
		return Config{}, err
	}
	if err := ApplyFlagOverrides(cmd, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func newPublishArtifactsCommand() *cobra.Command {
	var artifactSpecs []string
	var releaseVersion string
	cmd := &cobra.Command{
		Use:          "publish-artifacts",
		Short:        "Upload build artifacts to the configured S3/OSS bucket",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfigFromCommand(cmd)
			if err != nil {
				return err
			}
			publisher, err := artifacts.NewPublisher(cfg.Artifacts)
			if err != nil {
				return err
			}
			for _, spec := range artifactSpecs {
				localPath, objectKey, err := parseArtifactSpec(spec)
				if err != nil {
					return err
				}
				for _, targetKey := range publishObjectKeys(objectKey, releaseVersion) {
					published, err := publisher.UploadFile(cmd.Context(), localPath, targetKey)
					if err != nil {
						return err
					}
					if _, err := publisher.UploadSHA256(cmd.Context(), targetKey+".sha256", published.SHA256); err != nil {
						return err
					}
					log.Printf("published artifact local=%q object_key=%q size=%d sha256=%s url=%q", localPath, published.ObjectKey, published.SizeBytes, published.SHA256, published.PublicURL)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&artifactSpecs, "artifact", nil, "Artifact mapping in the form localPath=objectKey (repeatable)")
	cmd.Flags().StringVar(&releaseVersion, "version", "", "Release version used for versioned uploads and latest aliases")
	_ = cmd.MarkFlagRequired("artifact")
	return cmd
}

func publishObjectKeys(objectKey string, version string) []string {
	stable, latest, versioned := artifacts.ReleaseObjectKeys(objectKey, version)
	seen := make(map[string]struct{}, 3)
	keys := make([]string, 0, 3)
	for _, key := range []string{versioned, latest, stable} {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func parseArtifactSpec(value string) (string, string, error) {
	left, right, ok := strings.Cut(strings.TrimSpace(value), "=")
	localPath := strings.TrimSpace(left)
	objectKey := strings.Trim(strings.TrimSpace(right), "/")
	if !ok || localPath == "" || objectKey == "" {
		return "", "", errors.New("artifact must use localPath=objectKey")
	}
	return localPath, objectKey, nil
}

func Run(ctx context.Context, cfg Config) error {
	log.Printf("starting aiyolo gateway http_addr=%s read_timeout=%s write_timeout=%s idle_timeout=%s auto_migrate=%t seed_from_env=%t database=%q", cfg.HTTPAddr, cfg.ReadTimeout, cfg.WriteTimeout, cfg.IdleTimeout, cfg.AutoMigrate, cfg.SeedFromEnv, redactDatabaseURL(cfg.DatabaseURL))
	log.Printf("opening postgres database=%q", redactDatabaseURL(cfg.DatabaseURL))
	store, err := storage.OpenPostgres(ctx, cfg.DatabaseURL, cfg.SecretKey)
	if err != nil {
		return err
	}
	defer store.Close()
	log.Printf("postgres connected database=%q", redactDatabaseURL(cfg.DatabaseURL))
	if cfg.AutoMigrate {
		log.Printf("running database migrations")
		if err := store.Migrate(ctx); err != nil {
			return err
		}
		log.Printf("database migrations complete")
	}
	if cfg.SeedFromEnv {
		log.Printf("seeding default data")
		if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
			return err
		}
		log.Printf("default data seed complete")
		if cfg.SeedAPIKey != "" {
			log.Printf("ensuring seed api key exists")
			if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "seed", Name: "Seed API Key", KeyHash: auth.HashAPIKey(cfg.SeedAPIKey), Prefix: auth.Prefix(cfg.SeedAPIKey), UserID: "local", OrganizationID: "default", ProjectID: "default", Status: domain.StatusActive, CreatedAt: time.Now().UTC()}); err != nil {
				return err
			}
			log.Printf("seed api key created or updated")
		}
	}
	server := NewServer(cfg, store).HTTPServer()
	go func() {
		<-ctx.Done()
		log.Printf("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("http server shutdown err=%v", err)
			return
		}
		log.Printf("http server shutdown complete")
	}()
	log.Printf("AIYolo gateway listening on %s", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("http server stopped with error err=%v", err)
		return err
	}
	log.Printf("AIYolo gateway stopped")
	return nil
}

func redactDatabaseURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "configured"
	}
	if parsed.User != nil {
		if username := parsed.User.Username(); username != "" {
			parsed.User = url.User(username)
		} else {
			parsed.User = nil
		}
	}
	return parsed.String()
}
