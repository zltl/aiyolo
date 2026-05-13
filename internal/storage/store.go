package storage

import (
	"context"
	"errors"

	"github.com/zltl/aiyolo/internal/domain"
)

var ErrNotFound = errors.New("not found")
var ErrQuotaExceeded = errors.New("quota exceeded")

type Store interface {
	Close()
	Migrate(ctx context.Context) error
	SeedDefaults(ctx context.Context, seed SeedData) error

	CreateAPIKey(ctx context.Context, key domain.APIKey) error
	ListAPIKeys(ctx context.Context) ([]domain.APIKey, error)
	FindAPIKeyByHash(ctx context.Context, hash string) (domain.APIKey, error)
	TouchAPIKey(ctx context.Context, id string) error
	ReserveQuota(ctx context.Context, request domain.QuotaRequest) (domain.QuotaReservation, error)
	SettleQuota(ctx context.Context, reservation domain.QuotaReservation, usage domain.UsageRecord) error

	UpsertProvider(ctx context.Context, provider domain.Provider) error
	ListProviders(ctx context.Context) ([]domain.Provider, error)
	GetProvider(ctx context.Context, id string) (domain.Provider, error)

	UpsertModelRoute(ctx context.Context, route domain.ModelRoute) error
	ListModelRoutes(ctx context.Context) ([]domain.ModelRoute, error)
	GetModelRoute(ctx context.Context, publicName string) (domain.ModelRoute, error)

	UpsertProxyProfile(ctx context.Context, profile domain.ProxyProfile) error
	ListProxyProfiles(ctx context.Context) ([]domain.ProxyProfile, error)
	GetProxyProfile(ctx context.Context, id string) (domain.ProxyProfile, error)

	InsertUsage(ctx context.Context, usage domain.UsageRecord) error
	ListUsage(ctx context.Context, limit int) ([]domain.UsageRecord, error)
	InsertAudit(ctx context.Context, event domain.AuditEvent) error
	ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error)
	Dashboard(ctx context.Context) (domain.DashboardData, error)
}

type SeedData struct {
	OpenRouterKey  string
	DefaultBaseURL string
	DefaultModel   string
}
