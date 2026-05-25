package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

type MemoryStore struct {
	mu                 sync.RWMutex
	apiKeys            map[string]domain.APIKey
	codexTokens        map[string]domain.CodexInstallToken
	providers          map[string]domain.Provider
	routes             map[string]domain.ModelRoute
	pricingRules       map[string]domain.PricingRule
	proxies            map[string]domain.ProxyProfile
	workerSSHKeys      map[string]domain.WorkerSSHKey
	workers            map[string]domain.WorkerServer
	workerDisks        map[string][]domain.WorkerDataDisk
	workerJobs         map[string]domain.WorkerInitJob
	workerJobEvents    map[string][]domain.WorkerInitJobEvent
	cloudAgentAccounts map[string]domain.CloudAgentAccount
	cloudAgentSessions map[string]domain.CloudAgentSession
	consoleAuth        *domain.ConsoleAuthSettings
	chatSessions       map[string]domain.ConsoleChatSession
	usage              []domain.UsageRecord
	limits             map[string]memoryLimitWindow
	reservations       map[string]domain.QuotaReservation
}

type memoryLimitWindow struct {
	Requests int
	Tokens   int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		apiKeys:            make(map[string]domain.APIKey),
		codexTokens:        make(map[string]domain.CodexInstallToken),
		providers:          make(map[string]domain.Provider),
		routes:             make(map[string]domain.ModelRoute),
		pricingRules:       make(map[string]domain.PricingRule),
		proxies:            make(map[string]domain.ProxyProfile),
		workerSSHKeys:      make(map[string]domain.WorkerSSHKey),
		workers:            make(map[string]domain.WorkerServer),
		workerDisks:        make(map[string][]domain.WorkerDataDisk),
		workerJobs:         make(map[string]domain.WorkerInitJob),
		workerJobEvents:    make(map[string][]domain.WorkerInitJobEvent),
		cloudAgentAccounts: make(map[string]domain.CloudAgentAccount),
		cloudAgentSessions: make(map[string]domain.CloudAgentSession),
		chatSessions:       make(map[string]domain.ConsoleChatSession),
		limits:             make(map[string]memoryLimitWindow),
		reservations:       make(map[string]domain.QuotaReservation),
	}
}

func (store *MemoryStore) Close()                        {}
func (store *MemoryStore) Migrate(context.Context) error { return nil }

func (store *MemoryStore) SeedDefaults(ctx context.Context, seed SeedData) error {
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: domain.ProxyTypeDirect, Name: domain.ProxyTypeDirect, Type: domain.ProxyTypeDirect, Status: domain.StatusEnabled, TimeoutSeconds: 60}); err != nil {
		return err
	}
	return nil
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

func (store *MemoryStore) CreateCodexInstallToken(_ context.Context, token domain.CodexInstallToken) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	if token.CreatedAt.IsZero() {
		token.CreatedAt = now
	}
	if token.Platform == "" {
		token.Platform = "windows"
	}
	token.AllowedModels = nonNilStrings(token.AllowedModels)
	store.codexTokens[token.TokenHash] = token
	return nil
}

func (store *MemoryStore) RedeemCodexInstallToken(_ context.Context, tokenHash string, key domain.APIKey, redeemedAt time.Time) (domain.CodexInstallToken, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if redeemedAt.IsZero() {
		redeemedAt = time.Now().UTC()
	}
	token, ok := store.codexTokens[tokenHash]
	if !ok || token.UsedAt != nil || !redeemedAt.Before(token.ExpiresAt) {
		return domain.CodexInstallToken{}, ErrNotFound
	}
	if key.CreatedAt.IsZero() {
		key.CreatedAt = redeemedAt
	}
	key.AllowedProtocols = nonNilStrings(key.AllowedProtocols)
	key.AllowedModels = nonNilStrings(key.AllowedModels)
	for hash, existing := range store.apiKeys {
		if existing.ID == key.ID && hash != key.KeyHash {
			delete(store.apiKeys, hash)
		}
	}
	store.apiKeys[key.KeyHash] = key
	token.UsedAt = &redeemedAt
	token.APIKeyID = key.ID
	store.codexTokens[tokenHash] = token
	return token, nil
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
	provider.SupportedProtocols = nonNilStrings(domain.ProviderSupportedProtocols(provider))
	provider.Protocol = domain.ProviderPrimaryProtocol(provider)
	provider.TimeoutSeconds = domain.EffectiveProviderTimeoutSeconds(provider)
	provider.StreamIdleTimeoutSeconds = domain.EffectiveProviderStreamIdleTimeoutSeconds(provider)
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
	route.AllowedProtocols = nonNilStrings(domain.NormalizeProtocols(route.AllowedProtocols))
	route.Protocol = domain.NormalizeProtocol(route.Protocol)
	if len(route.AllowedProtocols) == 0 && route.Protocol != "" {
		route.AllowedProtocols = []string{route.Protocol}
	}
	if len(route.AllowedProtocols) == 0 {
		route.AllowedProtocols = []string{domain.ProtocolOpenAI}
	}
	if route.Protocol == "" {
		route.Protocol = route.AllowedProtocols[0]
	}
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

func (store *MemoryStore) LookupModelRoute(_ context.Context, publicName string) (domain.ModelRoute, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	route, ok := store.routes[publicName]
	if !ok {
		return domain.ModelRoute{}, ErrNotFound
	}
	return route, nil
}

func (store *MemoryStore) UpsertPricingRule(_ context.Context, rule domain.PricingRule) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.pricingRules[rule.ID]; ok {
		if rule.EffectiveFrom.IsZero() {
			rule.EffectiveFrom = existing.EffectiveFrom
		}
	}
	if rule.EffectiveFrom.IsZero() {
		rule.EffectiveFrom = time.Now().UTC()
	}
	if strings.TrimSpace(rule.Currency) == "" {
		rule.Currency = "USD"
	}
	store.pricingRules[rule.ID] = rule
	return nil
}

func (store *MemoryStore) ListPricingRules(context.Context) ([]domain.PricingRule, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	rules := make([]domain.PricingRule, 0, len(store.pricingRules))
	for _, rule := range store.pricingRules {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ModelAlias != rules[j].ModelAlias {
			return rules[i].ModelAlias < rules[j].ModelAlias
		}
		return rules[i].ID < rules[j].ID
	})
	return rules, nil
}

func (store *MemoryStore) GetPricingRule(_ context.Context, id string) (domain.PricingRule, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	rule, ok := store.pricingRules[id]
	if !ok {
		return domain.PricingRule{}, ErrNotFound
	}
	return rule, nil
}

func (store *MemoryStore) UpsertProxyProfile(_ context.Context, profile domain.ProxyProfile) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	normalized, err := domain.NormalizeProxyProfile(profile)
	if err != nil {
		return err
	}
	profile = normalized
	existing, ok := store.proxies[profile.ID]
	now := time.Now().UTC()
	if ok {
		profile.CreatedAt = existing.CreatedAt
		if profile.Auth == "" {
			profile.Auth = existing.Auth
		}
		if profile.LastError == "" {
			profile.LastError = existing.LastError
		}
	}
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

func (store *MemoryStore) GetUsageByRequestID(_ context.Context, requestID string) (domain.UsageRecord, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for index := len(store.usage) - 1; index >= 0; index-- {
		if store.usage[index].RequestID == requestID {
			return store.usage[index], nil
		}
	}
	return domain.UsageRecord{}, ErrNotFound
}

func (store *MemoryStore) SummarizeAPIKeyUsage(_ context.Context, apiKeyID string) (domain.APIKeyUsageSummary, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	now := time.Now().UTC()
	dailyStart := now.Add(-24 * time.Hour)
	weeklyStart := now.Add(-7 * 24 * time.Hour)
	monthlyStart := now.Add(-30 * 24 * time.Hour)
	var summary domain.APIKeyUsageSummary
	for _, usage := range store.usage {
		if usage.APIKeyID != apiKeyID {
			continue
		}
		addUsageTotals(&summary.AllTime, usage)
		if !usage.CreatedAt.Before(dailyStart) {
			addUsageTotals(&summary.Daily, usage)
		}
		if !usage.CreatedAt.Before(weeklyStart) {
			addUsageTotals(&summary.Weekly, usage)
		}
		if !usage.CreatedAt.Before(monthlyStart) {
			addUsageTotals(&summary.Monthly, usage)
		}
	}
	return summary, nil
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
	data.RecentUsage = recentUsage(store.usage, 10)
	return data, nil
}

func (store *MemoryStore) BillingOverview(context.Context) (domain.BillingOverview, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	var data domain.BillingOverview
	models := make(map[string]*domain.ModelCost)
	providers := make(map[string]*domain.SpendBreakdown)
	apiKeys := make(map[string]*domain.SpendBreakdown)
	users := make(map[string]*domain.SpendBreakdown)
	keyPrefixes := make(map[string]string, len(store.apiKeys))
	keyNames := make(map[string]string, len(store.apiKeys))
	for _, key := range store.apiKeys {
		keyPrefixes[key.ID] = key.Prefix
		keyNames[key.ID] = key.Name
	}
	for _, usage := range store.usage {
		data.RequestCount++
		if usage.Estimated {
			data.EstimatedCount++
		}
		data.InputTokens += int64(usage.InputTokens)
		data.OutputTokens += int64(usage.OutputTokens)
		data.CostMicroCents += usage.CostMicroCents

		model := models[usage.ModelAlias]
		if model == nil {
			model = &domain.ModelCost{ModelAlias: usage.ModelAlias}
			models[usage.ModelAlias] = model
		}
		model.RequestCount++
		model.InputTokens += int64(usage.InputTokens)
		model.OutputTokens += int64(usage.OutputTokens)
		model.CostMicroCents += usage.CostMicroCents

		provider := providers[usage.ProviderID]
		if provider == nil {
			provider = &domain.SpendBreakdown{Key: usage.ProviderID, Label: usage.ProviderID}
			providers[usage.ProviderID] = provider
		}
		provider.RequestCount++
		provider.TotalTokens += int64(usage.TotalTokens)
		provider.CostMicroCents += usage.CostMicroCents
		provider.LastSeen = laterTime(provider.LastSeen, usage.CreatedAt)

		keyID := usage.APIKeyID
		keyLabel := firstNonEmptyString(keyNames[keyID], keyPrefixes[keyID], keyID)
		keyCost := apiKeys[keyID]
		if keyCost == nil {
			keyCost = &domain.SpendBreakdown{Key: keyID, Label: keyLabel}
			apiKeys[keyID] = keyCost
		}
		keyCost.RequestCount++
		keyCost.TotalTokens += int64(usage.TotalTokens)
		keyCost.CostMicroCents += usage.CostMicroCents
		keyCost.LastSeen = laterTime(keyCost.LastSeen, usage.CreatedAt)

		userID := usage.UserID
		if userID == "" {
			userID = "anonymous"
		}
		userCost := users[userID]
		if userCost == nil {
			userCost = &domain.SpendBreakdown{Key: userID, Label: userID}
			users[userID] = userCost
		}
		userCost.RequestCount++
		userCost.TotalTokens += int64(usage.TotalTokens)
		userCost.CostMicroCents += usage.CostMicroCents
		userCost.LastSeen = laterTime(userCost.LastSeen, usage.CreatedAt)
	}
	for _, item := range models {
		data.ModelCosts = append(data.ModelCosts, *item)
	}
	data.ProviderCosts = spendBreakdownSlice(providers)
	data.APIKeyCosts = spendBreakdownSlice(apiKeys)
	data.UserCosts = spendBreakdownSlice(users)
	sort.Slice(data.ModelCosts, func(i, j int) bool { return data.ModelCosts[i].CostMicroCents > data.ModelCosts[j].CostMicroCents })
	data.RecentUsage = recentUsage(store.usage, 12)
	return data, nil
}

func (store *MemoryStore) UserDirectory(context.Context) (domain.UserDirectory, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	var data domain.UserDirectory
	if store.consoleAuth != nil {
		data.Settings = cloneConsoleAuthSettings(*store.consoleAuth)
	}
	data.ActiveAPIKeys = int64(len(store.apiKeys))
	data.ReadyOAuthProviders = int64(countReadyProviders(data.Settings))
	keyPrefixes := make(map[string]string, len(store.apiKeys))
	summaries := make(map[string]*domain.ConsoleUserSummary)
	for _, key := range store.apiKeys {
		keyPrefixes[key.ID] = key.Prefix
		userID := firstNonEmptyString(key.UserID, "anonymous")
		summary := summaries[userID]
		if summary == nil {
			summary = &domain.ConsoleUserSummary{UserID: userID}
			summaries[userID] = summary
		}
		summary.APIKeyCount++
		if summary.LastAPIKeyPrefix == "" {
			summary.LastAPIKeyPrefix = key.Prefix
		}
		summary.LastSeen = laterTime(summary.LastSeen, key.CreatedAt)
		if key.LastUsedAt != nil {
			summary.LastSeen = laterTime(summary.LastSeen, *key.LastUsedAt)
		}
	}
	for _, usage := range store.usage {
		userID := firstNonEmptyString(usage.UserID, "anonymous")
		summary := summaries[userID]
		if summary == nil {
			summary = &domain.ConsoleUserSummary{UserID: userID}
			summaries[userID] = summary
		}
		summary.RequestCount++
		summary.CostMicroCents += usage.CostMicroCents
		summary.LastSeen = laterTime(summary.LastSeen, usage.CreatedAt)
		if summary.LastAPIKeyPrefix == "" {
			summary.LastAPIKeyPrefix = keyPrefixes[usage.APIKeyID]
		}
	}
	for _, summary := range summaries {
		data.Summaries = append(data.Summaries, *summary)
	}
	sort.Slice(data.Summaries, func(i, j int) bool {
		left, right := data.Summaries[i], data.Summaries[j]
		if left.LastSeen != nil && right.LastSeen != nil && !left.LastSeen.Equal(*right.LastSeen) {
			return left.LastSeen.After(*right.LastSeen)
		}
		if left.RequestCount != right.RequestCount {
			return left.RequestCount > right.RequestCount
		}
		return left.UserID < right.UserID
	})
	data.ObservedUsers = int64(len(data.Summaries))
	return data, nil
}

func (store *MemoryStore) GetConsoleAuthSettings(context.Context) (domain.ConsoleAuthSettings, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.consoleAuth == nil {
		return domain.ConsoleAuthSettings{}, ErrNotFound
	}
	return cloneConsoleAuthSettings(*store.consoleAuth), nil
}

func (store *MemoryStore) SaveConsoleAuthSettings(_ context.Context, settings domain.ConsoleAuthSettings) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if settings.UpdatedAt.IsZero() {
		settings.UpdatedAt = time.Now().UTC()
	}
	cloned := cloneConsoleAuthSettings(settings)
	store.consoleAuth = &cloned
	return nil
}

func (store *MemoryStore) UpsertConsoleChatSession(_ context.Context, session domain.ConsoleChatSession) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	session = sanitizeConsoleChatSessionStrings(session)
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	store.chatSessions[consoleChatSessionStorageKey(session.UserID, session.ID)] = session
	return nil
}

func (store *MemoryStore) ListConsoleChatSessions(_ context.Context, userID string, limit int) ([]domain.ConsoleChatSession, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	sessions := make([]domain.ConsoleChatSession, 0, len(store.chatSessions))
	for _, session := range store.chatSessions {
		if session.UserID != userID {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		if !sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
		}
		return sessions[i].ID < sessions[j].ID
	})
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

func (store *MemoryStore) GetConsoleChatSession(_ context.Context, userID string, id string) (domain.ConsoleChatSession, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	session, ok := store.chatSessions[consoleChatSessionStorageKey(userID, id)]
	if !ok {
		return domain.ConsoleChatSession{}, ErrNotFound
	}
	return session, nil
}

func (store *MemoryStore) DeleteConsoleChatSession(_ context.Context, userID string, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := consoleChatSessionStorageKey(userID, id)
	if _, ok := store.chatSessions[key]; !ok {
		return ErrNotFound
	}
	delete(store.chatSessions, key)
	return nil
}

func consoleChatSessionStorageKey(userID, id string) string {
	return userID + "\x00" + id
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

func addUsageTotals(target *domain.UsageTotals, usage domain.UsageRecord) {
	target.Requests++
	target.TotalTokens += int64(usage.TotalTokens)
	target.CostMicroCents += usage.CostMicroCents
}

func cloneConsoleAuthSettings(settings domain.ConsoleAuthSettings) domain.ConsoleAuthSettings {
	clone := settings
	clone.AllowedEmails = append([]string(nil), settings.AllowedEmails...)
	clone.AllowedDomains = append([]string(nil), settings.AllowedDomains...)
	clone.Providers = make([]domain.OAuthProviderSettings, 0, len(settings.Providers))
	for _, provider := range settings.Providers {
		providerClone := provider
		providerClone.Scopes = append([]string(nil), provider.Scopes...)
		providerClone.AuthParams = append([]domain.KeyValue(nil), provider.AuthParams...)
		providerClone.TokenParams = append([]domain.KeyValue(nil), provider.TokenParams...)
		providerClone.UserInfoParams = append([]domain.KeyValue(nil), provider.UserInfoParams...)
		clone.Providers = append(clone.Providers, providerClone)
	}
	return clone
}

func spendBreakdownSlice(values map[string]*domain.SpendBreakdown) []domain.SpendBreakdown {
	items := make([]domain.SpendBreakdown, 0, len(values))
	for _, item := range values {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CostMicroCents != items[j].CostMicroCents {
			return items[i].CostMicroCents > items[j].CostMicroCents
		}
		return items[i].Label < items[j].Label
	})
	if len(items) > 8 {
		items = items[:8]
	}
	return items
}

func countReadyProviders(settings domain.ConsoleAuthSettings) int {
	count := 0
	for _, provider := range settings.Providers {
		if provider.Enabled && strings.TrimSpace(provider.ClientID) != "" && strings.TrimSpace(provider.ClientSecret) != "" && strings.TrimSpace(provider.AuthURL) != "" && strings.TrimSpace(provider.TokenURL) != "" && strings.TrimSpace(provider.UserInfoURL) != "" {
			count++
		}
	}
	return count
}

func laterTime(current *time.Time, candidate time.Time) *time.Time {
	if candidate.IsZero() {
		return current
	}
	if current == nil || candidate.After(*current) {
		value := candidate
		return &value
	}
	return current
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
