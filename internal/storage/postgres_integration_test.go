package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/storage"
)

func TestPostgresMigrateAndSeed(t *testing.T) {
	databaseURL := os.Getenv("AIYOLO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AIYOLO_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store, err := storage.OpenPostgres(ctx, databaseURL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaults(ctx, storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	providers, err := store.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := store.ListModelRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := store.ListProxyProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 || len(routes) != 0 || len(proxies) == 0 {
		t.Fatalf("unexpected seed result: providers=%d routes=%d proxies=%d", len(providers), len(routes), len(proxies))
	}
}
