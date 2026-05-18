package storage

import (
	"context"
	"errors"
	"time"

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
	CreateCodexInstallToken(ctx context.Context, token domain.CodexInstallToken) error
	RedeemCodexInstallToken(ctx context.Context, tokenHash string, key domain.APIKey, redeemedAt time.Time) (domain.CodexInstallToken, error)
	ReserveQuota(ctx context.Context, request domain.QuotaRequest) (domain.QuotaReservation, error)
	SettleQuota(ctx context.Context, reservation domain.QuotaReservation, usage domain.UsageRecord) error

	UpsertProvider(ctx context.Context, provider domain.Provider) error
	ListProviders(ctx context.Context) ([]domain.Provider, error)
	GetProvider(ctx context.Context, id string) (domain.Provider, error)

	UpsertModelRoute(ctx context.Context, route domain.ModelRoute) error
	ListModelRoutes(ctx context.Context) ([]domain.ModelRoute, error)
	GetModelRoute(ctx context.Context, publicName string) (domain.ModelRoute, error)
	LookupModelRoute(ctx context.Context, publicName string) (domain.ModelRoute, error)

	UpsertPricingRule(ctx context.Context, rule domain.PricingRule) error
	ListPricingRules(ctx context.Context) ([]domain.PricingRule, error)
	GetPricingRule(ctx context.Context, id string) (domain.PricingRule, error)

	UpsertProxyProfile(ctx context.Context, profile domain.ProxyProfile) error
	ListProxyProfiles(ctx context.Context) ([]domain.ProxyProfile, error)
	GetProxyProfile(ctx context.Context, id string) (domain.ProxyProfile, error)

	InsertUsage(ctx context.Context, usage domain.UsageRecord) error
	ListUsage(ctx context.Context, limit int) ([]domain.UsageRecord, error)
	GetUsageByRequestID(ctx context.Context, requestID string) (domain.UsageRecord, error)
	SummarizeAPIKeyUsage(ctx context.Context, apiKeyID string) (domain.APIKeyUsageSummary, error)
	InsertAudit(ctx context.Context, event domain.AuditEvent) error
	ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error)
	Dashboard(ctx context.Context) (domain.DashboardData, error)
	BillingOverview(ctx context.Context) (domain.BillingOverview, error)
	UserDirectory(ctx context.Context) (domain.UserDirectory, error)
	GetConsoleAuthSettings(ctx context.Context) (domain.ConsoleAuthSettings, error)
	SaveConsoleAuthSettings(ctx context.Context, settings domain.ConsoleAuthSettings) error
}

type SeedData struct{}
