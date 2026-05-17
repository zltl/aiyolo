package storage_test

import (
	"context"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestMemoryStoreSeedDefaultsSeedsDirectProxy(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	err := store.SeedDefaults(ctx, storage.SeedData{})
	if err != nil {
		t.Fatal(err)
	}

	profile, err := store.GetProxyProfile(ctx, domain.ProxyTypeDirect)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Endpoint != "" || profile.Type != domain.ProxyTypeDirect {
		t.Fatalf("unexpected proxy profile: %+v", profile)
	}
	providers, err := store.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 {
		t.Fatalf("providers=%d", len(providers))
	}
	routes, err := store.ListModelRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes=%d", len(routes))
	}
}