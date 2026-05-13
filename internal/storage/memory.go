package storage

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

type MemoryStore struct {
	mu           sync.RWMutex
	apiKeys      map[string]domain.APIKey
	providers    map[string]domain.Provider
	routes       map[string]domain.ModelRoute
	proxies      map[string]domain.ProxyProfile
	usage        []domain.UsageRecord
	audits       []domain.AuditEvent
	limits       map[string]memoryLimitWindow
	reservations map[string]domain.QuotaReservation
}

type memoryLimitWindow struct {
	Requests int
	Tokens   int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		apiKeys:      make(map[string]domain.APIKey),
		providers:    make(map[string]domain.Provider),
		routes:       make(map[string]domain.ModelRoute),
		proxies:      make(map[string]domain.ProxyProfile),
		limits:       make(map[string]memoryLimitWindow),
		reservations: make(map[string]domain.QuotaReservation),
	}
}

func (store *MemoryStore) Close()                        {}
func (store *MemoryStore) Migrate(context.Context) error { return nil }

func (store *MemoryStore) SeedDefaults(ctx context.Context, seed SeedData) error {
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: "direct", Name: "direct", Type: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 60}); err != nil {
		return err
	}
	if seed.OpenRouterKey == "" {
		return nil
	}
	provider := domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: seed.DefaultBaseURL, Protocol: domain.ProtocolOpenAI, MasterKey: seed.OpenRouterKey, Status: domain.StatusEnabled, TimeoutSeconds: 90, Weight: 100, Priority: 1}
	if err := store.UpsertProvider(ctx, provider); err != nil {
		return err
	}
	return store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: seed.DefaultModel, ProviderID: provider.ID, UpstreamModel: seed.DefaultModel, Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", Enabled: true, Weight: 100, Priority: 1})
}

func (store *MemoryStore) CreateAPIKey(_ context.Context, key domain.APIKey) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}
	for hash, existing := range store.apiKeys {
		if existing.ID == key.ID && hash != key.KeyHash {
			delete(store.apiKeys, hash)
		}
	}
	store.apiKeys[key.KeyHash] = key
	return nil
}

func (store *MemoryStore) ListAPIKeys(context.Context) ([]domain.APIKey, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	keys := make([]domain.APIKey, 0, len(store.apiKeys))
	for _, key := range store.apiKeys {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt.After(keys[j].CreatedAt) })
	return keys, nil
}

func (store *MemoryStore) FindAPIKeyByHash(_ context.Context, hash string) (domain.APIKey, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	key, ok := store.apiKeys[hash]
	if !ok {
		return domain.APIKey{}, ErrNotFound
	}
	return key, nil
}

func (store *MemoryStore) TouchAPIKey(_ context.Context, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	for hash, key := range store.apiKeys {
		if key.ID == id {
			key.LastUsedAt = &now
			store.apiKeys[hash] = key
			return nil
		}
	}
	return nil
}

func (store *MemoryStore) ReserveQuota(_ context.Context, request domain.QuotaRequest) (domain.QuotaReservation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if request.EstimatedTokens < 0 {
		request.EstimatedTokens = 0
	}
	active := 0
	for _, reservation := range store.reservations {
		if reservation.APIKeyID == request.APIKeyID && reservation.Status == "reserved" && reservation.CreatedAt.After(now.Add(-6*time.Hour)) {
			active++
		}
	}
	if request.ConcurrentLimit > 0 && active >= request.ConcurrentLimit {
		return domain.QuotaReservation{}, fmt.Errorf("%w: concurrent limit exceeded", ErrQuotaExceeded)
	}
	windowStart := now.Truncate(time.Minute)
	windowKey := request.APIKeyID + ":" + windowStart.Format(time.RFC3339)
	window := store.limits[windowKey]
	window.Requests++
	window.Tokens += request.EstimatedTokens
	if request.RPMLimit > 0 && window.Requests > request.RPMLimit {
		return domain.QuotaReservation{}, fmt.Errorf("%w: rpm limit exceeded", ErrQuotaExceeded)
	}
	if request.TPMLimit > 0 && window.Tokens > request.TPMLimit {
		return domain.QuotaReservation{}, fmt.Errorf("%w: tpm limit exceeded", ErrQuotaExceeded)
	}
	reservation := domain.QuotaReservation{
		ID:                      request.RequestID,
		RequestID:               request.RequestID,
		APIKeyID:                request.APIKeyID,
		UserID:                  request.UserID,
		ModelAlias:              request.ModelAlias,
		WindowStart:             windowStart,
		EstimatedTokens:         request.EstimatedTokens,
		EstimatedCostMicroCents: request.EstimatedCostMicroCents,
		Status:                  "reserved",
		CreatedAt:               now,
	}
	store.limits[windowKey] = window
	store.reservations[reservation.ID] = reservation
	return reservation, nil
}

func (store *MemoryStore) SettleQuota(_ context.Context, reservation domain.QuotaReservation, usage domain.UsageRecord) error {
	if reservation.ID == "" {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	stored, ok := store.reservations[reservation.ID]
	if !ok || stored.Status != "reserved" {
		return nil
	}
	actualTokens := actualUsageTokens(usage)
	windowKey := stored.APIKeyID + ":" + stored.WindowStart.Format(time.RFC3339)
	window := store.limits[windowKey]
	window.Tokens += actualTokens - stored.EstimatedTokens
	if window.Tokens < 0 {
		window.Tokens = 0
	}
	store.limits[windowKey] = window
	now := time.Now().UTC()
	stored.ActualTokens = actualTokens
	stored.ActualCostMicroCents = usage.CostMicroCents
	stored.Status = "settled"
	if usage.StatusCode >= 400 {
		stored.Status = "failed"
	}
	stored.SettledAt = &now
	store.reservations[reservation.ID] = stored
	return nil
}

func (store *MemoryStore) UpsertProvider(_ context.Context, provider domain.Provider) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = now
	}
	provider.UpdatedAt = now
	if provider.Status == "" {
		provider.Status = domain.StatusEnabled
	}
	store.providers[provider.ID] = provider
	return nil
}

func (store *MemoryStore) ListProviders(context.Context) ([]domain.Provider, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	providers := make([]domain.Provider, 0, len(store.providers))
	for _, provider := range store.providers {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	return providers, nil
}

func (store *MemoryStore) GetProvider(_ context.Context, id string) (domain.Provider, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	provider, ok := store.providers[id]
	if !ok {
		return domain.Provider{}, ErrNotFound
	}
	return provider, nil
}

func (store *MemoryStore) UpsertModelRoute(_ context.Context, route domain.ModelRoute) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	if route.CreatedAt.IsZero() {
		route.CreatedAt = now
	}
	route.UpdatedAt = now
	store.routes[route.PublicName] = route
	return nil
}

func (store *MemoryStore) ListModelRoutes(context.Context) ([]domain.ModelRoute, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	routes := make([]domain.ModelRoute, 0, len(store.routes))
	for _, route := range store.routes {
		if route.Enabled {
			routes = append(routes, route)
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].PublicName < routes[j].PublicName })
	return routes, nil
}

func (store *MemoryStore) GetModelRoute(_ context.Context, publicName string) (domain.ModelRoute, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	route, ok := store.routes[publicName]
	if !ok || !route.Enabled {
		return domain.ModelRoute{}, ErrNotFound
	}
	return route, nil
}

func (store *MemoryStore) UpsertProxyProfile(_ context.Context, profile domain.ProxyProfile) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	if profile.Status == "" {
		profile.Status = domain.StatusEnabled
	}
	store.proxies[profile.ID] = profile
	return nil
}

func (store *MemoryStore) ListProxyProfiles(context.Context) ([]domain.ProxyProfile, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	profiles := make([]domain.ProxyProfile, 0, len(store.proxies))
	for _, profile := range store.proxies {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles, nil
}

func (store *MemoryStore) GetProxyProfile(_ context.Context, id string) (domain.ProxyProfile, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	profile, ok := store.proxies[id]
	if !ok {
		return domain.ProxyProfile{}, ErrNotFound
	}
	return profile, nil
}

func (store *MemoryStore) InsertUsage(_ context.Context, usage domain.UsageRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = time.Now().UTC()
	}
	store.usage = append(store.usage, usage)
	return nil
}

func (store *MemoryStore) ListUsage(_ context.Context, limit int) ([]domain.UsageRecord, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return recentUsage(store.usage, limit), nil
}

func (store *MemoryStore) InsertAudit(_ context.Context, event domain.AuditEvent) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	store.audits = append(store.audits, event)
	return nil
}

func (store *MemoryStore) ListAudit(_ context.Context, limit int) ([]domain.AuditEvent, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return recentAudit(store.audits, limit), nil
}

func (store *MemoryStore) Dashboard(context.Context) (domain.DashboardData, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	var data domain.DashboardData
	models := make(map[string]*domain.ModelCost)
	for _, usage := range store.usage {
		data.RequestCount++
		if usage.StatusCode >= 400 {
			data.ErrorCount++
		}
		if usage.Estimated {
			data.EstimatedCount++
		}
		data.InputTokens += int64(usage.InputTokens)
		data.OutputTokens += int64(usage.OutputTokens)
		data.CostMicroCents += usage.CostMicroCents
		cost := models[usage.ModelAlias]
		if cost == nil {
			cost = &domain.ModelCost{ModelAlias: usage.ModelAlias}
			models[usage.ModelAlias] = cost
		}
		cost.RequestCount++
		cost.InputTokens += int64(usage.InputTokens)
		cost.OutputTokens += int64(usage.OutputTokens)
		cost.CostMicroCents += usage.CostMicroCents
	}
	for _, cost := range models {
		data.ModelCosts = append(data.ModelCosts, *cost)
	}
	sort.Slice(data.ModelCosts, func(i, j int) bool { return data.ModelCosts[i].CostMicroCents > data.ModelCosts[j].CostMicroCents })
	data.RecentAudits = recentAudit(store.audits, 10)
	data.RecentUsage = recentUsage(store.usage, 10)
	return data, nil
}

func recentUsage(values []domain.UsageRecord, limit int) []domain.UsageRecord {
	if limit <= 0 || limit > len(values) {
		limit = len(values)
	}
	result := make([]domain.UsageRecord, 0, limit)
	for i := len(values) - 1; i >= 0 && len(result) < limit; i-- {
		result = append(result, values[i])
	}
	return result
}

func recentAudit(values []domain.AuditEvent, limit int) []domain.AuditEvent {
	if limit <= 0 || limit > len(values) {
		limit = len(values)
	}
	result := make([]domain.AuditEvent, 0, limit)
	for i := len(values) - 1; i >= 0 && len(result) < limit; i-- {
		result = append(result, values[i])
	}
	return result
}
