package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zltl/aiyolo/internal/domain"
)

type PostgresStore struct {
	pool        *pgxpool.Pool
	box         SecretBox
	schema      string
	databaseURL string
}

func OpenPostgres(ctx context.Context, databaseURL, secretKey string) (*PostgresStore, error) {
	schema, cleanURL, err := extractSchema(databaseURL)
	if err != nil {
		return nil, err
	}
	config, err := pgxpool.ParseConfig(cleanURL)
	if err != nil {
		return nil, err
	}
	if schema != "" {
		config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET search_path TO "+quoteIdent(schema)+", public")
			return err
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool, box: NewSecretBox(secretKey), schema: schema, databaseURL: cleanURL}, nil
}

func (store *PostgresStore) Close() { store.pool.Close() }

func (store *PostgresStore) Migrate(ctx context.Context) error {
	return runMigrations(ctx, store.databaseURL, store.schema)
}

func extractSchema(databaseURL string) (string, string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", "", err
	}
	query := parsed.Query()
	schema := strings.TrimSpace(query.Get("aiyolo_schema"))
	if schema != "" && !validIdentifier(schema) {
		return "", "", fmt.Errorf("invalid aiyolo_schema")
	}
	query.Del("aiyolo_schema")
	parsed.RawQuery = query.Encode()
	return schema, parsed.String(), nil
}

func validIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if index == 0 {
			if !(char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z') {
				return false
			}
			continue
		}
		if !(char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (store *PostgresStore) SeedDefaults(ctx context.Context, seed SeedData) error {
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: "direct", Name: "direct", Type: "direct", Status: domain.StatusEnabled, TimeoutSeconds: 60}); err != nil {
		return err
	}
	if strings.TrimSpace(seed.OpenRouterKey) == "" {
		return nil
	}
	provider := domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: seed.DefaultBaseURL, Protocol: domain.ProtocolOpenAI, MasterKey: seed.OpenRouterKey, DefaultProxyID: "direct", Status: domain.StatusEnabled, Priority: 1, Weight: 100, TimeoutSeconds: 90}
	if err := store.UpsertProvider(ctx, provider); err != nil {
		return err
	}
	return store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: seed.DefaultModel, ProviderID: provider.ID, UpstreamModel: seed.DefaultModel, Protocol: domain.ProtocolOpenAI, ProxyProfileID: "direct", Enabled: true, Priority: 1, Weight: 100})
}

func (store *PostgresStore) CreateAPIKey(ctx context.Context, key domain.APIKey) error {
	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}
	key.AllowedProtocols = nonNilStrings(key.AllowedProtocols)
	key.AllowedModels = nonNilStrings(key.AllowedModels)
	_, err := store.pool.Exec(ctx, `
INSERT INTO api_keys (id, name, key_hash, prefix, user_id, organization_id, project_id, status, allowed_protocols, allowed_models, rpm_limit, tpm_limit, concurrent_limit, daily_budget_cents, monthly_budget_cents, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, key_hash = excluded.key_hash, prefix = excluded.prefix, user_id = excluded.user_id, organization_id = excluded.organization_id, project_id = excluded.project_id, status = excluded.status, allowed_protocols = excluded.allowed_protocols, allowed_models = excluded.allowed_models, rpm_limit = excluded.rpm_limit, tpm_limit = excluded.tpm_limit, concurrent_limit = excluded.concurrent_limit, daily_budget_cents = excluded.daily_budget_cents, monthly_budget_cents = excluded.monthly_budget_cents, expires_at = excluded.expires_at`,
		key.ID, key.Name, key.KeyHash, key.Prefix, key.UserID, key.OrganizationID, key.ProjectID, normalizeStatus(key.Status, domain.StatusActive), key.AllowedProtocols, key.AllowedModels, key.RPMLimit, key.TPMLimit, key.ConcurrentLimit, key.DailyBudgetCents, key.MonthlyBudgetCents, key.ExpiresAt, key.CreatedAt)
	return err
}

func (store *PostgresStore) ListAPIKeys(ctx context.Context) ([]domain.APIKey, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name, key_hash, prefix, user_id, organization_id, project_id, status, allowed_protocols, allowed_models, rpm_limit, tpm_limit, concurrent_limit, daily_budget_cents, monthly_budget_cents, expires_at, created_at, last_used_at FROM api_keys ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []domain.APIKey
	for rows.Next() {
		var key domain.APIKey
		if err := rows.Scan(&key.ID, &key.Name, &key.KeyHash, &key.Prefix, &key.UserID, &key.OrganizationID, &key.ProjectID, &key.Status, &key.AllowedProtocols, &key.AllowedModels, &key.RPMLimit, &key.TPMLimit, &key.ConcurrentLimit, &key.DailyBudgetCents, &key.MonthlyBudgetCents, &key.ExpiresAt, &key.CreatedAt, &key.LastUsedAt); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (store *PostgresStore) FindAPIKeyByHash(ctx context.Context, hash string) (domain.APIKey, error) {
	var key domain.APIKey
	err := store.pool.QueryRow(ctx, `SELECT id, name, key_hash, prefix, user_id, organization_id, project_id, status, allowed_protocols, allowed_models, rpm_limit, tpm_limit, concurrent_limit, daily_budget_cents, monthly_budget_cents, expires_at, created_at, last_used_at FROM api_keys WHERE key_hash = $1`, hash).Scan(&key.ID, &key.Name, &key.KeyHash, &key.Prefix, &key.UserID, &key.OrganizationID, &key.ProjectID, &key.Status, &key.AllowedProtocols, &key.AllowedModels, &key.RPMLimit, &key.TPMLimit, &key.ConcurrentLimit, &key.DailyBudgetCents, &key.MonthlyBudgetCents, &key.ExpiresAt, &key.CreatedAt, &key.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.APIKey{}, ErrNotFound
	}
	return key, err
}

func (store *PostgresStore) TouchAPIKey(ctx context.Context, id string) error {
	_, err := store.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, id)
	return err
}

func (store *PostgresStore) ReserveQuota(ctx context.Context, request domain.QuotaRequest) (domain.QuotaReservation, error) {
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if request.EstimatedTokens < 0 {
		request.EstimatedTokens = 0
	}
	reservation := domain.QuotaReservation{
		ID:                      request.RequestID,
		RequestID:               request.RequestID,
		APIKeyID:                request.APIKeyID,
		UserID:                  request.UserID,
		ModelAlias:              request.ModelAlias,
		WindowStart:             now.Truncate(time.Minute),
		EstimatedTokens:         request.EstimatedTokens,
		EstimatedCostMicroCents: request.EstimatedCostMicroCents,
		Status:                  "reserved",
		CreatedAt:               now,
	}
	if reservation.ID == "" {
		return domain.QuotaReservation{}, fmt.Errorf("quota reservation requires request id")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.QuotaReservation{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, request.APIKeyID); err != nil {
		return domain.QuotaReservation{}, err
	}
	if request.ConcurrentLimit > 0 {
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM quota_reservations WHERE api_key_id = $1 AND status = 'reserved' AND created_at >= $2`, request.APIKeyID, now.Add(-6*time.Hour)).Scan(&active); err != nil {
			return domain.QuotaReservation{}, err
		}
		if active >= request.ConcurrentLimit {
			return domain.QuotaReservation{}, fmt.Errorf("%w: concurrent limit exceeded", ErrQuotaExceeded)
		}
	}
	if request.RPMLimit > 0 || request.TPMLimit > 0 {
		var requestCount int
		var tokenCount int
		if err := tx.QueryRow(ctx, `
INSERT INTO rate_limit_windows (scope, scope_id, window_start, request_count, token_count, updated_at)
VALUES ('api_key', $1, $2, 1, $3, now())
ON CONFLICT (scope, scope_id, window_start)
DO UPDATE SET request_count = rate_limit_windows.request_count + 1, token_count = rate_limit_windows.token_count + EXCLUDED.token_count, updated_at = now()
RETURNING request_count, token_count`, request.APIKeyID, reservation.WindowStart, request.EstimatedTokens).Scan(&requestCount, &tokenCount); err != nil {
			return domain.QuotaReservation{}, err
		}
		if request.RPMLimit > 0 && requestCount > request.RPMLimit {
			return domain.QuotaReservation{}, fmt.Errorf("%w: rpm limit exceeded", ErrQuotaExceeded)
		}
		if request.TPMLimit > 0 && tokenCount > request.TPMLimit {
			return domain.QuotaReservation{}, fmt.Errorf("%w: tpm limit exceeded", ErrQuotaExceeded)
		}
	}
	if request.EstimatedCostMicroCents > 0 {
		if request.DailyBudgetCents > 0 {
			spent, err := quotaSpentSince(ctx, tx, request.APIKeyID, startOfDay(now))
			if err != nil {
				return domain.QuotaReservation{}, err
			}
			if spent+request.EstimatedCostMicroCents > request.DailyBudgetCents*1_000_000 {
				return domain.QuotaReservation{}, fmt.Errorf("%w: daily budget exceeded", ErrQuotaExceeded)
			}
		}
		if request.MonthlyBudgetCents > 0 {
			spent, err := quotaSpentSince(ctx, tx, request.APIKeyID, startOfMonth(now))
			if err != nil {
				return domain.QuotaReservation{}, err
			}
			if spent+request.EstimatedCostMicroCents > request.MonthlyBudgetCents*1_000_000 {
				return domain.QuotaReservation{}, fmt.Errorf("%w: monthly budget exceeded", ErrQuotaExceeded)
			}
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO quota_reservations (id, request_id, api_key_id, user_id, model_alias, window_start, estimated_tokens, estimated_cost_micro_cents, status, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, reservation.ID, reservation.RequestID, reservation.APIKeyID, reservation.UserID, reservation.ModelAlias, reservation.WindowStart, reservation.EstimatedTokens, reservation.EstimatedCostMicroCents, reservation.Status, reservation.CreatedAt); err != nil {
		return domain.QuotaReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.QuotaReservation{}, err
	}
	return reservation, nil
}

func (store *PostgresStore) SettleQuota(ctx context.Context, reservation domain.QuotaReservation, usage domain.UsageRecord) error {
	if reservation.ID == "" {
		return nil
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, reservation.APIKeyID); err != nil {
		return err
	}
	var stored domain.QuotaReservation
	err = tx.QueryRow(ctx, `SELECT id, request_id, api_key_id, user_id, model_alias, window_start, estimated_tokens, estimated_cost_micro_cents, status, created_at, settled_at FROM quota_reservations WHERE id = $1 FOR UPDATE`, reservation.ID).Scan(&stored.ID, &stored.RequestID, &stored.APIKeyID, &stored.UserID, &stored.ModelAlias, &stored.WindowStart, &stored.EstimatedTokens, &stored.EstimatedCostMicroCents, &stored.Status, &stored.CreatedAt, &stored.SettledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if stored.Status != "reserved" {
		return nil
	}
	actualTokens := actualUsageTokens(usage)
	deltaTokens := actualTokens - stored.EstimatedTokens
	if _, err := tx.Exec(ctx, `UPDATE rate_limit_windows SET token_count = greatest(0, token_count + $1), updated_at = now() WHERE scope = 'api_key' AND scope_id = $2 AND window_start = $3`, deltaTokens, stored.APIKeyID, stored.WindowStart); err != nil {
		return err
	}
	status := "settled"
	if usage.StatusCode >= 400 {
		status = "failed"
	}
	if _, err := tx.Exec(ctx, `UPDATE quota_reservations SET actual_tokens = $1, actual_cost_micro_cents = $2, status = $3, settled_at = now() WHERE id = $4`, actualTokens, usage.CostMicroCents, status, stored.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) UpsertProvider(ctx context.Context, provider domain.Provider) error {
	secret, err := store.box.Encrypt(provider.MasterKey)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = now
	}
	provider.UpdatedAt = now
	_, err = store.pool.Exec(ctx, `
INSERT INTO providers (id, name, base_url, protocol, master_key_ciphertext, default_proxy_id, priority, weight, status, timeout_seconds, rate_limit_hint, last_health_check, last_error, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, base_url = excluded.base_url, protocol = excluded.protocol, master_key_ciphertext = CASE WHEN excluded.master_key_ciphertext = '' THEN providers.master_key_ciphertext ELSE excluded.master_key_ciphertext END, default_proxy_id = excluded.default_proxy_id, priority = excluded.priority, weight = excluded.weight, status = excluded.status, timeout_seconds = excluded.timeout_seconds, rate_limit_hint = excluded.rate_limit_hint, last_health_check = excluded.last_health_check, last_error = excluded.last_error, updated_at = excluded.updated_at`,
		provider.ID, provider.Name, provider.BaseURL, provider.Protocol, secret, provider.DefaultProxyID, provider.Priority, provider.Weight, normalizeStatus(provider.Status, domain.StatusEnabled), provider.TimeoutSeconds, provider.RateLimitHint, provider.LastHealthCheck, provider.LastError, provider.CreatedAt, provider.UpdatedAt)
	return err
}

func (store *PostgresStore) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name, base_url, protocol, default_proxy_id, priority, weight, status, timeout_seconds, rate_limit_hint, last_health_check, last_error, created_at, updated_at FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []domain.Provider
	for rows.Next() {
		var provider domain.Provider
		if err := rows.Scan(&provider.ID, &provider.Name, &provider.BaseURL, &provider.Protocol, &provider.DefaultProxyID, &provider.Priority, &provider.Weight, &provider.Status, &provider.TimeoutSeconds, &provider.RateLimitHint, &provider.LastHealthCheck, &provider.LastError, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

func (store *PostgresStore) GetProvider(ctx context.Context, id string) (domain.Provider, error) {
	var provider domain.Provider
	var secret string
	err := store.pool.QueryRow(ctx, `SELECT id, name, base_url, protocol, master_key_ciphertext, default_proxy_id, priority, weight, status, timeout_seconds, rate_limit_hint, last_health_check, last_error, created_at, updated_at FROM providers WHERE id = $1`, id).Scan(&provider.ID, &provider.Name, &provider.BaseURL, &provider.Protocol, &secret, &provider.DefaultProxyID, &provider.Priority, &provider.Weight, &provider.Status, &provider.TimeoutSeconds, &provider.RateLimitHint, &provider.LastHealthCheck, &provider.LastError, &provider.CreatedAt, &provider.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Provider{}, ErrNotFound
	}
	if err != nil {
		return domain.Provider{}, err
	}
	provider.MasterKey, err = store.box.Decrypt(secret)
	return provider, err
}

func (store *PostgresStore) UpsertModelRoute(ctx context.Context, route domain.ModelRoute) error {
	now := time.Now().UTC()
	if route.CreatedAt.IsZero() {
		route.CreatedAt = now
	}
	route.UpdatedAt = now
	_, err := store.pool.Exec(ctx, `
INSERT INTO model_routes (public_name, provider_id, upstream_model, protocol, proxy_profile_id, price_rule_id, enabled, priority, weight, context_tokens, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (public_name) DO UPDATE SET provider_id = excluded.provider_id, upstream_model = excluded.upstream_model, protocol = excluded.protocol, proxy_profile_id = excluded.proxy_profile_id, price_rule_id = excluded.price_rule_id, enabled = excluded.enabled, priority = excluded.priority, weight = excluded.weight, context_tokens = excluded.context_tokens, updated_at = excluded.updated_at`,
		route.PublicName, route.ProviderID, route.UpstreamModel, route.Protocol, route.ProxyProfileID, route.PriceRuleID, route.Enabled, route.Priority, route.Weight, route.ContextTokens, route.CreatedAt, route.UpdatedAt)
	return err
}

func (store *PostgresStore) ListModelRoutes(ctx context.Context) ([]domain.ModelRoute, error) {
	rows, err := store.pool.Query(ctx, `SELECT public_name, provider_id, upstream_model, protocol, proxy_profile_id, price_rule_id, enabled, priority, weight, context_tokens, created_at, updated_at FROM model_routes WHERE enabled = true ORDER BY public_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []domain.ModelRoute
	for rows.Next() {
		var route domain.ModelRoute
		if err := rows.Scan(&route.PublicName, &route.ProviderID, &route.UpstreamModel, &route.Protocol, &route.ProxyProfileID, &route.PriceRuleID, &route.Enabled, &route.Priority, &route.Weight, &route.ContextTokens, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (store *PostgresStore) GetModelRoute(ctx context.Context, publicName string) (domain.ModelRoute, error) {
	var route domain.ModelRoute
	err := store.pool.QueryRow(ctx, `SELECT public_name, provider_id, upstream_model, protocol, proxy_profile_id, price_rule_id, enabled, priority, weight, context_tokens, created_at, updated_at FROM model_routes WHERE public_name = $1 AND enabled = true`, publicName).Scan(&route.PublicName, &route.ProviderID, &route.UpstreamModel, &route.Protocol, &route.ProxyProfileID, &route.PriceRuleID, &route.Enabled, &route.Priority, &route.Weight, &route.ContextTokens, &route.CreatedAt, &route.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ModelRoute{}, ErrNotFound
	}
	return route, err
}

func (store *PostgresStore) UpsertProxyProfile(ctx context.Context, profile domain.ProxyProfile) error {
	secret, err := store.box.Encrypt(profile.Auth)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	_, err = store.pool.Exec(ctx, `
INSERT INTO proxy_profiles (id, name, type, endpoint, auth_ciphertext, region, timeout_seconds, health_check_url, status, last_error, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, type = excluded.type, endpoint = excluded.endpoint, auth_ciphertext = CASE WHEN excluded.auth_ciphertext = '' THEN proxy_profiles.auth_ciphertext ELSE excluded.auth_ciphertext END, region = excluded.region, timeout_seconds = excluded.timeout_seconds, health_check_url = excluded.health_check_url, status = excluded.status, last_error = excluded.last_error, updated_at = excluded.updated_at`,
		profile.ID, profile.Name, profile.Type, profile.Endpoint, secret, profile.Region, profile.TimeoutSeconds, profile.HealthCheckURL, normalizeStatus(profile.Status, domain.StatusEnabled), profile.LastError, profile.CreatedAt, profile.UpdatedAt)
	return err
}

func (store *PostgresStore) ListProxyProfiles(ctx context.Context) ([]domain.ProxyProfile, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name, type, endpoint, region, timeout_seconds, health_check_url, status, last_error, created_at, updated_at FROM proxy_profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []domain.ProxyProfile
	for rows.Next() {
		var profile domain.ProxyProfile
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.Type, &profile.Endpoint, &profile.Region, &profile.TimeoutSeconds, &profile.HealthCheckURL, &profile.Status, &profile.LastError, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (store *PostgresStore) GetProxyProfile(ctx context.Context, id string) (domain.ProxyProfile, error) {
	var profile domain.ProxyProfile
	var secret string
	err := store.pool.QueryRow(ctx, `SELECT id, name, type, endpoint, auth_ciphertext, region, timeout_seconds, health_check_url, status, last_error, created_at, updated_at FROM proxy_profiles WHERE id = $1`, id).Scan(&profile.ID, &profile.Name, &profile.Type, &profile.Endpoint, &secret, &profile.Region, &profile.TimeoutSeconds, &profile.HealthCheckURL, &profile.Status, &profile.LastError, &profile.CreatedAt, &profile.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProxyProfile{}, ErrNotFound
	}
	if err != nil {
		return domain.ProxyProfile{}, err
	}
	profile.Auth, err = store.box.Decrypt(secret)
	return profile, err
}

func (store *PostgresStore) InsertUsage(ctx context.Context, usage domain.UsageRecord) error {
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = time.Now().UTC()
	}
	_, err := store.pool.Exec(ctx, `INSERT INTO usage_ledger (request_id, user_id, api_key_id, provider_id, model_alias, upstream_model, protocol, endpoint, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, cost_micro_cents, currency, estimated, stream, status_code, latency_ms, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, usage.RequestID, usage.UserID, usage.APIKeyID, usage.ProviderID, usage.ModelAlias, usage.UpstreamModel, usage.Protocol, usage.Endpoint, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheCreationTokens, usage.TotalTokens, usage.CostMicroCents, usage.Currency, usage.Estimated, usage.Stream, usage.StatusCode, usage.LatencyMS, usage.CreatedAt)
	return err
}

func (store *PostgresStore) ListUsage(ctx context.Context, limit int) ([]domain.UsageRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.pool.Query(ctx, `SELECT request_id, user_id, api_key_id, provider_id, model_alias, upstream_model, protocol, endpoint, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, cost_micro_cents, currency, estimated, stream, status_code, latency_ms, created_at FROM usage_ledger ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var usage []domain.UsageRecord
	for rows.Next() {
		var item domain.UsageRecord
		if err := rows.Scan(&item.RequestID, &item.UserID, &item.APIKeyID, &item.ProviderID, &item.ModelAlias, &item.UpstreamModel, &item.Protocol, &item.Endpoint, &item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheCreationTokens, &item.TotalTokens, &item.CostMicroCents, &item.Currency, &item.Estimated, &item.Stream, &item.StatusCode, &item.LatencyMS, &item.CreatedAt); err != nil {
			return nil, err
		}
		usage = append(usage, item)
	}
	return usage, rows.Err()
}

func (store *PostgresStore) InsertAudit(ctx context.Context, event domain.AuditEvent) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := store.pool.Exec(ctx, `INSERT INTO audit_logs (id, request_id, trace_id, user_id, api_key_id, client_ip, user_agent, protocol, endpoint, model_alias, provider_id, upstream_model, proxy_profile_id, status_code, error_code, latency_ms, input_tokens, output_tokens, cost_micro_cents, stream, event_type, message, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, event.ID, event.RequestID, event.TraceID, event.UserID, event.APIKeyID, event.ClientIP, event.UserAgent, event.Protocol, event.Endpoint, event.ModelAlias, event.ProviderID, event.UpstreamModel, event.ProxyProfileID, event.StatusCode, event.ErrorCode, event.LatencyMS, event.InputTokens, event.OutputTokens, event.CostMicroCents, event.Stream, event.EventType, event.Message, event.CreatedAt)
	return err
}

func (store *PostgresStore) ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.pool.Query(ctx, `SELECT id, request_id, trace_id, user_id, api_key_id, client_ip, user_agent, protocol, endpoint, model_alias, provider_id, upstream_model, proxy_profile_id, status_code, error_code, latency_ms, input_tokens, output_tokens, cost_micro_cents, stream, event_type, message, created_at FROM audit_logs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.AuditEvent
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(&event.ID, &event.RequestID, &event.TraceID, &event.UserID, &event.APIKeyID, &event.ClientIP, &event.UserAgent, &event.Protocol, &event.Endpoint, &event.ModelAlias, &event.ProviderID, &event.UpstreamModel, &event.ProxyProfileID, &event.StatusCode, &event.ErrorCode, &event.LatencyMS, &event.InputTokens, &event.OutputTokens, &event.CostMicroCents, &event.Stream, &event.EventType, &event.Message, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (store *PostgresStore) Dashboard(ctx context.Context) (domain.DashboardData, error) {
	var data domain.DashboardData
	err := store.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status_code >= 400), count(*) FILTER (WHERE estimated), coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0), coalesce(sum(cost_micro_cents),0) FROM usage_ledger WHERE created_at >= now() - interval '24 hours'`).Scan(&data.RequestCount, &data.ErrorCount, &data.EstimatedCount, &data.InputTokens, &data.OutputTokens, &data.CostMicroCents)
	if err != nil {
		return data, err
	}
	data.RecentAudits, err = store.ListAudit(ctx, 10)
	if err != nil {
		return data, err
	}
	data.RecentUsage, err = store.ListUsage(ctx, 10)
	if err != nil {
		return data, err
	}
	rows, err := store.pool.Query(ctx, `SELECT model_alias, count(*), coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0), coalesce(sum(cost_micro_cents),0) FROM usage_ledger WHERE created_at >= now() - interval '24 hours' GROUP BY model_alias ORDER BY 5 DESC LIMIT 10`)
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.ModelCost
		if err := rows.Scan(&item.ModelAlias, &item.RequestCount, &item.InputTokens, &item.OutputTokens, &item.CostMicroCents); err != nil {
			return data, err
		}
		data.ModelCosts = append(data.ModelCosts, item)
	}
	return data, rows.Err()
}

func normalizeStatus(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
