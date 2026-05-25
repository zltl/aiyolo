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
	Dashboard(ctx context.Context) (domain.DashboardData, error)
	BillingOverview(ctx context.Context) (domain.BillingOverview, error)
	UserDirectory(ctx context.Context) (domain.UserDirectory, error)
	GetConsoleAuthSettings(ctx context.Context) (domain.ConsoleAuthSettings, error)
	SaveConsoleAuthSettings(ctx context.Context, settings domain.ConsoleAuthSettings) error
	UpsertConsoleChatSession(ctx context.Context, session domain.ConsoleChatSession) error
	ListConsoleChatSessions(ctx context.Context, userID string, limit int) ([]domain.ConsoleChatSession, error)
	GetConsoleChatSession(ctx context.Context, userID string, id string) (domain.ConsoleChatSession, error)
	DeleteConsoleChatSession(ctx context.Context, userID string, id string) error

	UpsertWorkerSSHKey(ctx context.Context, key domain.WorkerSSHKey) error
	ListWorkerSSHKeys(ctx context.Context) ([]domain.WorkerSSHKey, error)
	GetWorkerSSHKey(ctx context.Context, id string) (domain.WorkerSSHKey, error)

	UpsertWorkerServer(ctx context.Context, worker domain.WorkerServer) error
	ListWorkerServers(ctx context.Context) ([]domain.WorkerServer, error)
	GetWorkerServer(ctx context.Context, id string) (domain.WorkerServer, error)
	ReplaceWorkerDataDisks(ctx context.Context, workerID string, disks []domain.WorkerDataDisk) error
	ListWorkerDataDisks(ctx context.Context, workerID string) ([]domain.WorkerDataDisk, error)

	UpsertWorkerInitJob(ctx context.Context, job domain.WorkerInitJob) error
	ListWorkerInitJobs(ctx context.Context, workerID string, limit int) ([]domain.WorkerInitJob, error)
	GetWorkerInitJob(ctx context.Context, workerID string, jobID string) (domain.WorkerInitJob, error)
	AppendWorkerInitJobEvent(ctx context.Context, event domain.WorkerInitJobEvent) error
	ListWorkerInitJobEvents(ctx context.Context, workerID string, jobID string, afterSequence int64) ([]domain.WorkerInitJobEvent, error)

	UpsertCloudAgentAccount(ctx context.Context, account domain.CloudAgentAccount) error
	ListCloudAgentAccounts(ctx context.Context, userID string, workerID string) ([]domain.CloudAgentAccount, error)
	GetCloudAgentAccount(ctx context.Context, userID string, accountID string) (domain.CloudAgentAccount, error)

	UpsertCloudAgentSession(ctx context.Context, session domain.CloudAgentSession) error
	ListCloudAgentSessions(ctx context.Context, userID string, workerID string, limit int) ([]domain.CloudAgentSession, error)
	GetCloudAgentSession(ctx context.Context, userID string, sessionID string) (domain.CloudAgentSession, error)
}

type SeedData struct{}
