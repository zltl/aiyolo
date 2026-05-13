package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/spf13/cobra"

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
	AddConfigFlags(cmd.Flags())
	return cmd
}

func runServe(cmd *cobra.Command, _ []string) error {
	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return err
	}
	v, _, err := NewViper(configFile)
	if err != nil {
		return err
	}
	if err := BindStringFlags(v, cmd.Flags()); err != nil {
		return err
	}
	cfg, err := LoadConfig(v)
	if err != nil {
		return err
	}
	if err := ApplyFlagOverrides(cmd, &cfg); err != nil {
		return err
	}
	return Run(cmd.Context(), cfg)
}

func Run(ctx context.Context, cfg Config) error {
	store, err := storage.OpenPostgres(ctx, cfg.DatabaseURL, cfg.SecretKey)
	if err != nil {
		return err
	}
	defer store.Close()
	if cfg.AutoMigrate {
		if err := store.Migrate(ctx); err != nil {
			return err
		}
	}
	if cfg.SeedFromEnv {
		if err := store.SeedDefaults(ctx, storage.SeedData{OpenRouterKey: cfg.OpenRouterKey, DefaultBaseURL: cfg.DefaultBaseURL, DefaultModel: cfg.DefaultModel}); err != nil {
			return err
		}
		if cfg.SeedAPIKey != "" {
			if err := store.CreateAPIKey(ctx, domain.APIKey{ID: "seed", Name: "Seed API Key", KeyHash: auth.HashAPIKey(cfg.SeedAPIKey), Prefix: auth.Prefix(cfg.SeedAPIKey), UserID: "local", OrganizationID: "default", ProjectID: "default", Status: domain.StatusActive, CreatedAt: time.Now().UTC()}); err != nil {
				return err
			}
		}
	}
	server := NewServer(cfg, store).HTTPServer()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("AIYolo gateway listening on %s", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
