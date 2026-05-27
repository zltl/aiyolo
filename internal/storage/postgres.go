package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

const consoleAuthSettingsKey = "console_auth"

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
	if err := store.UpsertProxyProfile(ctx, domain.ProxyProfile{ID: domain.ProxyTypeDirect, Name: domain.ProxyTypeDirect, Type: domain.ProxyTypeDirect, Status: domain.StatusEnabled, TimeoutSeconds: 60}); err != nil {
		return err
	}
	return nil
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

func (store *PostgresStore) CreateCodexInstallToken(ctx context.Context, token domain.CodexInstallToken) error {
	if token.CreatedAt.IsZero() {
		token.CreatedAt = time.Now().UTC()
	}
	if token.Platform == "" {
		token.Platform = "windows"
	}
	token.AllowedModels = nonNilStrings(token.AllowedModels)
	_, err := store.pool.Exec(ctx, `
INSERT INTO codex_install_tokens (id, token_hash, created_by, platform, default_model, allowed_models, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (id) DO UPDATE SET token_hash = excluded.token_hash, created_by = excluded.created_by, platform = excluded.platform, default_model = excluded.default_model, allowed_models = excluded.allowed_models, expires_at = excluded.expires_at, created_at = excluded.created_at, used_at = NULL, api_key_id = ''`,
		token.ID, token.TokenHash, token.CreatedBy, token.Platform, token.DefaultModel, token.AllowedModels, token.ExpiresAt, token.CreatedAt)
	return err
}

func (store *PostgresStore) RedeemCodexInstallToken(ctx context.Context, tokenHash string, key domain.APIKey, redeemedAt time.Time) (domain.CodexInstallToken, error) {
	if redeemedAt.IsZero() {
		redeemedAt = time.Now().UTC()
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.CodexInstallToken{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var token domain.CodexInstallToken
	err = tx.QueryRow(ctx, `SELECT id, token_hash, created_by, platform, default_model, allowed_models, expires_at, created_at, used_at, api_key_id FROM codex_install_tokens WHERE token_hash = $1 FOR UPDATE`, tokenHash).Scan(&token.ID, &token.TokenHash, &token.CreatedBy, &token.Platform, &token.DefaultModel, &token.AllowedModels, &token.ExpiresAt, &token.CreatedAt, &token.UsedAt, &token.APIKeyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CodexInstallToken{}, ErrNotFound
	}
	if err != nil {
		return domain.CodexInstallToken{}, err
	}
	if token.UsedAt != nil || !redeemedAt.Before(token.ExpiresAt) {
		return domain.CodexInstallToken{}, ErrNotFound
	}
	if key.CreatedAt.IsZero() {
		key.CreatedAt = redeemedAt
	}
	key.AllowedProtocols = nonNilStrings(key.AllowedProtocols)
	key.AllowedModels = nonNilStrings(key.AllowedModels)
	_, err = tx.Exec(ctx, `
INSERT INTO api_keys (id, name, key_hash, prefix, user_id, organization_id, project_id, status, allowed_protocols, allowed_models, rpm_limit, tpm_limit, concurrent_limit, daily_budget_cents, monthly_budget_cents, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, key_hash = excluded.key_hash, prefix = excluded.prefix, user_id = excluded.user_id, organization_id = excluded.organization_id, project_id = excluded.project_id, status = excluded.status, allowed_protocols = excluded.allowed_protocols, allowed_models = excluded.allowed_models, rpm_limit = excluded.rpm_limit, tpm_limit = excluded.tpm_limit, concurrent_limit = excluded.concurrent_limit, daily_budget_cents = excluded.daily_budget_cents, monthly_budget_cents = excluded.monthly_budget_cents, expires_at = excluded.expires_at`,
		key.ID, key.Name, key.KeyHash, key.Prefix, key.UserID, key.OrganizationID, key.ProjectID, normalizeStatus(key.Status, domain.StatusActive), key.AllowedProtocols, key.AllowedModels, key.RPMLimit, key.TPMLimit, key.ConcurrentLimit, key.DailyBudgetCents, key.MonthlyBudgetCents, key.ExpiresAt, key.CreatedAt)
	if err != nil {
		return domain.CodexInstallToken{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE codex_install_tokens SET used_at = $1, api_key_id = $2 WHERE id = $3`, redeemedAt, key.ID, token.ID)
	if err != nil {
		return domain.CodexInstallToken{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.CodexInstallToken{}, err
	}
	token.UsedAt = &redeemedAt
	token.APIKeyID = key.ID
	return token, nil
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
	provider.SupportedProtocols = nonNilStrings(domain.ProviderSupportedProtocols(provider))
	provider.Protocol = domain.ProviderPrimaryProtocol(provider)
	provider.TimeoutSeconds = domain.EffectiveProviderTimeoutSeconds(provider)
	provider.StreamIdleTimeoutSeconds = domain.EffectiveProviderStreamIdleTimeoutSeconds(provider)
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
INSERT INTO providers (id, name, base_url, protocol, supported_protocols, master_key_ciphertext, default_proxy_id, priority, weight, status, timeout_seconds, stream_idle_timeout_seconds, rate_limit_hint, last_health_check, last_error, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, base_url = excluded.base_url, protocol = excluded.protocol, supported_protocols = excluded.supported_protocols, master_key_ciphertext = CASE WHEN excluded.master_key_ciphertext = '' THEN providers.master_key_ciphertext ELSE excluded.master_key_ciphertext END, default_proxy_id = excluded.default_proxy_id, priority = excluded.priority, weight = excluded.weight, status = excluded.status, timeout_seconds = excluded.timeout_seconds, stream_idle_timeout_seconds = excluded.stream_idle_timeout_seconds, rate_limit_hint = excluded.rate_limit_hint, last_health_check = excluded.last_health_check, last_error = excluded.last_error, updated_at = excluded.updated_at`,
		provider.ID, provider.Name, provider.BaseURL, provider.Protocol, provider.SupportedProtocols, secret, provider.DefaultProxyID, provider.Priority, provider.Weight, normalizeStatus(provider.Status, domain.StatusEnabled), provider.TimeoutSeconds, provider.StreamIdleTimeoutSeconds, provider.RateLimitHint, provider.LastHealthCheck, provider.LastError, provider.CreatedAt, provider.UpdatedAt)
	return err
}

func (store *PostgresStore) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name, base_url, protocol, supported_protocols, default_proxy_id, priority, weight, status, timeout_seconds, stream_idle_timeout_seconds, rate_limit_hint, last_health_check, last_error, created_at, updated_at FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []domain.Provider
	for rows.Next() {
		var provider domain.Provider
		if err := rows.Scan(&provider.ID, &provider.Name, &provider.BaseURL, &provider.Protocol, &provider.SupportedProtocols, &provider.DefaultProxyID, &provider.Priority, &provider.Weight, &provider.Status, &provider.TimeoutSeconds, &provider.StreamIdleTimeoutSeconds, &provider.RateLimitHint, &provider.LastHealthCheck, &provider.LastError, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

func (store *PostgresStore) GetProvider(ctx context.Context, id string) (domain.Provider, error) {
	var provider domain.Provider
	var secret string
	err := store.pool.QueryRow(ctx, `SELECT id, name, base_url, protocol, supported_protocols, master_key_ciphertext, default_proxy_id, priority, weight, status, timeout_seconds, stream_idle_timeout_seconds, rate_limit_hint, last_health_check, last_error, created_at, updated_at FROM providers WHERE id = $1`, id).Scan(&provider.ID, &provider.Name, &provider.BaseURL, &provider.Protocol, &provider.SupportedProtocols, &secret, &provider.DefaultProxyID, &provider.Priority, &provider.Weight, &provider.Status, &provider.TimeoutSeconds, &provider.StreamIdleTimeoutSeconds, &provider.RateLimitHint, &provider.LastHealthCheck, &provider.LastError, &provider.CreatedAt, &provider.UpdatedAt)
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
	now := time.Now().UTC()
	if route.CreatedAt.IsZero() {
		route.CreatedAt = now
	}
	route.UpdatedAt = now
	_, err := store.pool.Exec(ctx, `
INSERT INTO model_routes (public_name, provider_id, upstream_model, protocol, allowed_protocols, proxy_profile_id, price_rule_id, enabled, priority, weight, context_tokens, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (public_name) DO UPDATE SET provider_id = excluded.provider_id, upstream_model = excluded.upstream_model, protocol = excluded.protocol, allowed_protocols = excluded.allowed_protocols, proxy_profile_id = excluded.proxy_profile_id, price_rule_id = excluded.price_rule_id, enabled = excluded.enabled, priority = excluded.priority, weight = excluded.weight, context_tokens = excluded.context_tokens, updated_at = excluded.updated_at`,
		route.PublicName, route.ProviderID, route.UpstreamModel, route.Protocol, route.AllowedProtocols, route.ProxyProfileID, route.PriceRuleID, route.Enabled, route.Priority, route.Weight, route.ContextTokens, route.CreatedAt, route.UpdatedAt)
	return err
}

func (store *PostgresStore) ListModelRoutes(ctx context.Context) ([]domain.ModelRoute, error) {
	rows, err := store.pool.Query(ctx, `SELECT public_name, provider_id, upstream_model, protocol, allowed_protocols, proxy_profile_id, price_rule_id, enabled, priority, weight, context_tokens, created_at, updated_at FROM model_routes WHERE enabled = true ORDER BY public_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []domain.ModelRoute
	for rows.Next() {
		var route domain.ModelRoute
		if err := rows.Scan(&route.PublicName, &route.ProviderID, &route.UpstreamModel, &route.Protocol, &route.AllowedProtocols, &route.ProxyProfileID, &route.PriceRuleID, &route.Enabled, &route.Priority, &route.Weight, &route.ContextTokens, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (store *PostgresStore) GetModelRoute(ctx context.Context, publicName string) (domain.ModelRoute, error) {
	var route domain.ModelRoute
	err := store.pool.QueryRow(ctx, `SELECT public_name, provider_id, upstream_model, protocol, allowed_protocols, proxy_profile_id, price_rule_id, enabled, priority, weight, context_tokens, created_at, updated_at FROM model_routes WHERE public_name = $1 AND enabled = true`, publicName).Scan(&route.PublicName, &route.ProviderID, &route.UpstreamModel, &route.Protocol, &route.AllowedProtocols, &route.ProxyProfileID, &route.PriceRuleID, &route.Enabled, &route.Priority, &route.Weight, &route.ContextTokens, &route.CreatedAt, &route.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ModelRoute{}, ErrNotFound
	}
	return route, err
}

func (store *PostgresStore) LookupModelRoute(ctx context.Context, publicName string) (domain.ModelRoute, error) {
	var route domain.ModelRoute
	err := store.pool.QueryRow(ctx, `SELECT public_name, provider_id, upstream_model, protocol, allowed_protocols, proxy_profile_id, price_rule_id, enabled, priority, weight, context_tokens, created_at, updated_at FROM model_routes WHERE public_name = $1`, publicName).Scan(&route.PublicName, &route.ProviderID, &route.UpstreamModel, &route.Protocol, &route.AllowedProtocols, &route.ProxyProfileID, &route.PriceRuleID, &route.Enabled, &route.Priority, &route.Weight, &route.ContextTokens, &route.CreatedAt, &route.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ModelRoute{}, ErrNotFound
	}
	return route, err
}

func (store *PostgresStore) UpsertPricingRule(ctx context.Context, rule domain.PricingRule) error {
	if strings.TrimSpace(rule.Currency) == "" {
		rule.Currency = domain.DefaultBillingCurrency
	}
	if rule.EffectiveFrom.IsZero() {
		rule.EffectiveFrom = time.Now().UTC()
	}
	_, err := store.pool.Exec(ctx, `
INSERT INTO pricing_rules (id, model_alias, provider_id, currency, input_price_per_million_tokens, output_price_per_million_tokens, cache_read_price_per_million_tokens, cache_write_price_per_million_tokens, effective_from, effective_to)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (id) DO UPDATE SET model_alias = excluded.model_alias, provider_id = excluded.provider_id, currency = excluded.currency, input_price_per_million_tokens = excluded.input_price_per_million_tokens, output_price_per_million_tokens = excluded.output_price_per_million_tokens, cache_read_price_per_million_tokens = excluded.cache_read_price_per_million_tokens, cache_write_price_per_million_tokens = excluded.cache_write_price_per_million_tokens, effective_from = excluded.effective_from, effective_to = excluded.effective_to`,
		rule.ID, rule.ModelAlias, rule.ProviderID, rule.Currency, rule.InputPricePerMillionTokens, rule.OutputPricePerMillionTokens, rule.CacheReadPricePerMillionTokens, rule.CacheWritePricePerMillionTokens, rule.EffectiveFrom, rule.EffectiveTo)
	return err
}

func (store *PostgresStore) ListPricingRules(ctx context.Context) ([]domain.PricingRule, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, model_alias, provider_id, currency, input_price_per_million_tokens, output_price_per_million_tokens, cache_read_price_per_million_tokens, cache_write_price_per_million_tokens, effective_from, effective_to FROM pricing_rules ORDER BY model_alias, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []domain.PricingRule
	for rows.Next() {
		var rule domain.PricingRule
		if err := rows.Scan(&rule.ID, &rule.ModelAlias, &rule.ProviderID, &rule.Currency, &rule.InputPricePerMillionTokens, &rule.OutputPricePerMillionTokens, &rule.CacheReadPricePerMillionTokens, &rule.CacheWritePricePerMillionTokens, &rule.EffectiveFrom, &rule.EffectiveTo); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (store *PostgresStore) GetPricingRule(ctx context.Context, id string) (domain.PricingRule, error) {
	var rule domain.PricingRule
	err := store.pool.QueryRow(ctx, `SELECT id, model_alias, provider_id, currency, input_price_per_million_tokens, output_price_per_million_tokens, cache_read_price_per_million_tokens, cache_write_price_per_million_tokens, effective_from, effective_to FROM pricing_rules WHERE id = $1`, id).Scan(&rule.ID, &rule.ModelAlias, &rule.ProviderID, &rule.Currency, &rule.InputPricePerMillionTokens, &rule.OutputPricePerMillionTokens, &rule.CacheReadPricePerMillionTokens, &rule.CacheWritePricePerMillionTokens, &rule.EffectiveFrom, &rule.EffectiveTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PricingRule{}, ErrNotFound
	}
	return rule, err
}

func (store *PostgresStore) UpsertProxyProfile(ctx context.Context, profile domain.ProxyProfile) error {
	normalized, err := domain.NormalizeProxyProfile(profile)
	if err != nil {
		return err
	}
	profile = normalized
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
INSERT INTO proxy_profiles (id, name, type, endpoint, auth_ciphertext, region, timeout_seconds, stream_idle_timeout_seconds, health_check_url, status, last_error, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, type = excluded.type, endpoint = excluded.endpoint, auth_ciphertext = CASE WHEN excluded.auth_ciphertext = '' THEN proxy_profiles.auth_ciphertext ELSE excluded.auth_ciphertext END, region = excluded.region, timeout_seconds = excluded.timeout_seconds, stream_idle_timeout_seconds = excluded.stream_idle_timeout_seconds, health_check_url = excluded.health_check_url, status = excluded.status, last_error = CASE WHEN excluded.last_error = '' THEN proxy_profiles.last_error ELSE excluded.last_error END, updated_at = excluded.updated_at`,
		profile.ID, profile.Name, profile.Type, profile.Endpoint, secret, profile.Region, profile.TimeoutSeconds, profile.StreamIdleTimeoutSeconds, profile.HealthCheckURL, normalizeStatus(profile.Status, domain.StatusEnabled), profile.LastError, profile.CreatedAt, profile.UpdatedAt)
	return err
}

func (store *PostgresStore) ListProxyProfiles(ctx context.Context) ([]domain.ProxyProfile, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name, type, endpoint, region, timeout_seconds, stream_idle_timeout_seconds, health_check_url, status, last_error, created_at, updated_at FROM proxy_profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []domain.ProxyProfile
	for rows.Next() {
		var profile domain.ProxyProfile
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.Type, &profile.Endpoint, &profile.Region, &profile.TimeoutSeconds, &profile.StreamIdleTimeoutSeconds, &profile.HealthCheckURL, &profile.Status, &profile.LastError, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (store *PostgresStore) GetProxyProfile(ctx context.Context, id string) (domain.ProxyProfile, error) {
	var profile domain.ProxyProfile
	var secret string
	err := store.pool.QueryRow(ctx, `SELECT id, name, type, endpoint, auth_ciphertext, region, timeout_seconds, stream_idle_timeout_seconds, health_check_url, status, last_error, created_at, updated_at FROM proxy_profiles WHERE id = $1`, id).Scan(&profile.ID, &profile.Name, &profile.Type, &profile.Endpoint, &secret, &profile.Region, &profile.TimeoutSeconds, &profile.StreamIdleTimeoutSeconds, &profile.HealthCheckURL, &profile.Status, &profile.LastError, &profile.CreatedAt, &profile.UpdatedAt)
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

func (store *PostgresStore) GetUsageByRequestID(ctx context.Context, requestID string) (domain.UsageRecord, error) {
	var item domain.UsageRecord
	err := store.pool.QueryRow(ctx, `SELECT request_id, user_id, api_key_id, provider_id, model_alias, upstream_model, protocol, endpoint, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, cost_micro_cents, currency, estimated, stream, status_code, latency_ms, created_at FROM usage_ledger WHERE request_id = $1 ORDER BY created_at DESC LIMIT 1`, requestID).Scan(&item.RequestID, &item.UserID, &item.APIKeyID, &item.ProviderID, &item.ModelAlias, &item.UpstreamModel, &item.Protocol, &item.Endpoint, &item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheCreationTokens, &item.TotalTokens, &item.CostMicroCents, &item.Currency, &item.Estimated, &item.Stream, &item.StatusCode, &item.LatencyMS, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UsageRecord{}, ErrNotFound
	}
	return item, err
}

func (store *PostgresStore) SummarizeAPIKeyUsage(ctx context.Context, apiKeyID string) (domain.APIKeyUsageSummary, error) {
	var summary domain.APIKeyUsageSummary
	err := store.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE api_key_id = $1),
			coalesce(sum(total_tokens) FILTER (WHERE api_key_id = $1), 0),
			coalesce(sum(cost_micro_cents) FILTER (WHERE api_key_id = $1), 0),
			count(*) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '1 day'),
			coalesce(sum(total_tokens) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '1 day'), 0),
			coalesce(sum(cost_micro_cents) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '1 day'), 0),
			count(*) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '7 days'),
			coalesce(sum(total_tokens) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '7 days'), 0),
			coalesce(sum(cost_micro_cents) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '7 days'), 0),
			count(*) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '30 days'),
			coalesce(sum(total_tokens) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '30 days'), 0),
			coalesce(sum(cost_micro_cents) FILTER (WHERE api_key_id = $1 AND created_at >= now() - interval '30 days'), 0)
		FROM usage_ledger`, apiKeyID).Scan(
		&summary.AllTime.Requests,
		&summary.AllTime.TotalTokens,
		&summary.AllTime.CostMicroCents,
		&summary.Daily.Requests,
		&summary.Daily.TotalTokens,
		&summary.Daily.CostMicroCents,
		&summary.Weekly.Requests,
		&summary.Weekly.TotalTokens,
		&summary.Weekly.CostMicroCents,
		&summary.Monthly.Requests,
		&summary.Monthly.TotalTokens,
		&summary.Monthly.CostMicroCents,
	)
	return summary, err
}

func (store *PostgresStore) Dashboard(ctx context.Context) (domain.DashboardData, error) {
	var data domain.DashboardData
	err := store.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status_code >= 400), count(*) FILTER (WHERE estimated), coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0), coalesce(sum(cost_micro_cents),0) FROM usage_ledger WHERE created_at >= now() - interval '24 hours'`).Scan(&data.RequestCount, &data.ErrorCount, &data.EstimatedCount, &data.InputTokens, &data.OutputTokens, &data.CostMicroCents)
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

func (store *PostgresStore) BillingOverview(ctx context.Context) (domain.BillingOverview, error) {
	var data domain.BillingOverview
	err := store.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE estimated), coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0), coalesce(sum(cost_micro_cents),0) FROM usage_ledger WHERE created_at >= now() - interval '30 days'`).Scan(&data.RequestCount, &data.EstimatedCount, &data.InputTokens, &data.OutputTokens, &data.CostMicroCents)
	if err != nil {
		return data, err
	}
	data.RecentUsage, err = store.ListUsage(ctx, 12)
	if err != nil {
		return data, err
	}
	if data.ModelCosts, err = queryModelCosts(ctx, store.pool); err != nil {
		return data, err
	}
	if data.ProviderCosts, err = querySpendBreakdowns(ctx, store.pool, `SELECT provider_id AS key, provider_id AS label, count(*), coalesce(sum(total_tokens),0), coalesce(sum(cost_micro_cents),0), max(created_at) FROM usage_ledger WHERE created_at >= now() - interval '30 days' GROUP BY provider_id ORDER BY 5 DESC LIMIT 8`); err != nil {
		return data, err
	}
	if data.APIKeyCosts, err = querySpendBreakdowns(ctx, store.pool, `SELECT l.api_key_id AS key, coalesce(nullif(k.name, ''), nullif(k.prefix, ''), l.api_key_id) AS label, count(*), coalesce(sum(l.total_tokens),0), coalesce(sum(l.cost_micro_cents),0), max(l.created_at) FROM usage_ledger l LEFT JOIN api_keys k ON k.id = l.api_key_id WHERE l.created_at >= now() - interval '30 days' GROUP BY l.api_key_id, label ORDER BY 5 DESC LIMIT 8`); err != nil {
		return data, err
	}
	if data.UserCosts, err = querySpendBreakdowns(ctx, store.pool, `SELECT coalesce(nullif(user_id, ''), 'anonymous') AS key, coalesce(nullif(user_id, ''), 'anonymous') AS label, count(*), coalesce(sum(total_tokens),0), coalesce(sum(cost_micro_cents),0), max(created_at) FROM usage_ledger WHERE created_at >= now() - interval '30 days' GROUP BY 1, 2 ORDER BY 5 DESC LIMIT 8`); err != nil {
		return data, err
	}
	return data, nil
}

func (store *PostgresStore) UserDirectory(ctx context.Context) (domain.UserDirectory, error) {
	var data domain.UserDirectory
	settings, err := store.GetConsoleAuthSettings(ctx)
	if err == nil {
		data.Settings = settings
	} else if !errors.Is(err, ErrNotFound) {
		return data, err
	}
	data.ReadyOAuthProviders = int64(countReadyAuthProviders(data.Settings))
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM api_keys`).Scan(&data.ActiveAPIKeys); err != nil {
		return data, err
	}
	keys, err := store.ListAPIKeys(ctx)
	if err != nil {
		return data, err
	}
	usageRows, err := store.pool.Query(ctx, `SELECT coalesce(nullif(user_id, ''), 'anonymous') AS user_id, count(*), coalesce(sum(cost_micro_cents),0), max(created_at) FROM usage_ledger GROUP BY 1 ORDER BY max(created_at) DESC`)
	if err != nil {
		return data, err
	}
	defer usageRows.Close()
	summaries := make(map[string]*domain.ConsoleUserSummary)
	for _, key := range keys {
		userID := firstNonEmptyValue(key.UserID, "anonymous")
		summary := summaries[userID]
		if summary == nil {
			summary = &domain.ConsoleUserSummary{UserID: userID}
			summaries[userID] = summary
		}
		summary.APIKeyCount++
		if summary.LastAPIKeyPrefix == "" {
			summary.LastAPIKeyPrefix = key.Prefix
		}
		summary.LastSeen = maxTime(summary.LastSeen, key.CreatedAt)
		if key.LastUsedAt != nil {
			summary.LastSeen = maxTime(summary.LastSeen, *key.LastUsedAt)
		}
	}
	for usageRows.Next() {
		var userID string
		var requestCount int64
		var cost int64
		var lastSeen time.Time
		if err := usageRows.Scan(&userID, &requestCount, &cost, &lastSeen); err != nil {
			return data, err
		}
		summary := summaries[userID]
		if summary == nil {
			summary = &domain.ConsoleUserSummary{UserID: userID}
			summaries[userID] = summary
		}
		summary.RequestCount = requestCount
		summary.CostMicroCents = cost
		summary.LastSeen = maxTime(summary.LastSeen, lastSeen)
	}
	if err := usageRows.Err(); err != nil {
		return data, err
	}
	for _, summary := range summaries {
		data.Summaries = append(data.Summaries, *summary)
	}
	sortConsoleUsers(data.Summaries)
	data.ObservedUsers = int64(len(data.Summaries))
	return data, nil
}

func (store *PostgresStore) GetConsoleAuthSettings(ctx context.Context) (domain.ConsoleAuthSettings, error) {
	var secret string
	err := store.pool.QueryRow(ctx, `SELECT value_ciphertext FROM console_settings WHERE key = $1`, consoleAuthSettingsKey).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ConsoleAuthSettings{}, ErrNotFound
	}
	if err != nil {
		return domain.ConsoleAuthSettings{}, err
	}
	if strings.TrimSpace(secret) == "" {
		return domain.ConsoleAuthSettings{}, ErrNotFound
	}
	clear, err := store.box.Decrypt(secret)
	if err != nil {
		return domain.ConsoleAuthSettings{}, err
	}
	var settings domain.ConsoleAuthSettings
	if err := json.Unmarshal([]byte(clear), &settings); err != nil {
		return domain.ConsoleAuthSettings{}, err
	}
	return settings, nil
}

func (store *PostgresStore) SaveConsoleAuthSettings(ctx context.Context, settings domain.ConsoleAuthSettings) error {
	if settings.UpdatedAt.IsZero() {
		settings.UpdatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	secret, err := store.box.Encrypt(string(payload))
	if err != nil {
		return err
	}
	_, err = store.pool.Exec(ctx, `
INSERT INTO console_settings (key, value_ciphertext, updated_at)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE SET value_ciphertext = excluded.value_ciphertext, updated_at = excluded.updated_at`, consoleAuthSettingsKey, secret, settings.UpdatedAt)
	return err
}

func (store *PostgresStore) UpsertConsoleChatSession(ctx context.Context, session domain.ConsoleChatSession) error {
	session = sanitizeConsoleChatSessionStrings(session)
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	_, err := store.pool.Exec(ctx, `
INSERT INTO console_chat_sessions (user_id, id, title, custom_title, public_name, system_prompt, draft, draft_attachments_json, status, messages_json, message_count, last_request_id, last_response_id, last_error, created_at, updated_at, last_message_at, completed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (user_id, id) DO UPDATE SET title = excluded.title, custom_title = excluded.custom_title, public_name = excluded.public_name, system_prompt = excluded.system_prompt, draft = excluded.draft, draft_attachments_json = excluded.draft_attachments_json, status = excluded.status, messages_json = excluded.messages_json, message_count = excluded.message_count, last_request_id = excluded.last_request_id, last_response_id = excluded.last_response_id, last_error = excluded.last_error, updated_at = excluded.updated_at, last_message_at = excluded.last_message_at, completed_at = excluded.completed_at`,
		session.UserID, session.ID, session.Title, session.CustomTitle, session.PublicName, session.SystemPrompt, session.Draft, session.DraftAttachmentsJSON, session.Status, session.MessagesJSON, session.MessageCount, session.LastRequestID, session.LastResponseID, session.LastError, session.CreatedAt, session.UpdatedAt, session.LastMessageAt, session.CompletedAt)
	return err
}

func sanitizeConsoleChatSessionStrings(session domain.ConsoleChatSession) domain.ConsoleChatSession {
	replacement := string(utf8.RuneError)
	session.ID = strings.ToValidUTF8(session.ID, replacement)
	session.UserID = strings.ToValidUTF8(session.UserID, replacement)
	session.Title = strings.ToValidUTF8(session.Title, replacement)
	session.PublicName = strings.ToValidUTF8(session.PublicName, replacement)
	session.SystemPrompt = strings.ToValidUTF8(session.SystemPrompt, replacement)
	session.Draft = strings.ToValidUTF8(session.Draft, replacement)
	session.DraftAttachmentsJSON = strings.ToValidUTF8(session.DraftAttachmentsJSON, replacement)
	session.Status = strings.ToValidUTF8(session.Status, replacement)
	session.MessagesJSON = strings.ToValidUTF8(session.MessagesJSON, replacement)
	session.LastRequestID = strings.ToValidUTF8(session.LastRequestID, replacement)
	session.LastResponseID = strings.ToValidUTF8(session.LastResponseID, replacement)
	session.LastError = strings.ToValidUTF8(session.LastError, replacement)
	return session
}

func (store *PostgresStore) ListConsoleChatSessions(ctx context.Context, userID string, limit int) ([]domain.ConsoleChatSession, error) {
	if limit <= 0 {
		limit = 24
	}
	rows, err := store.pool.Query(ctx, `SELECT user_id, id, title, custom_title, public_name, system_prompt, draft, draft_attachments_json, status, messages_json, message_count, last_request_id, last_response_id, last_error, created_at, updated_at, last_message_at, completed_at FROM console_chat_sessions WHERE user_id = $1 ORDER BY updated_at DESC, created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ConsoleChatSession, 0, limit)
	for rows.Next() {
		item, err := scanConsoleChatSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) GetConsoleChatSession(ctx context.Context, userID string, id string) (domain.ConsoleChatSession, error) {
	row := store.pool.QueryRow(ctx, `SELECT user_id, id, title, custom_title, public_name, system_prompt, draft, draft_attachments_json, status, messages_json, message_count, last_request_id, last_response_id, last_error, created_at, updated_at, last_message_at, completed_at FROM console_chat_sessions WHERE user_id = $1 AND id = $2`, userID, id)
	item, err := scanConsoleChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ConsoleChatSession{}, ErrNotFound
	}
	return item, err
}

func (store *PostgresStore) DeleteConsoleChatSession(ctx context.Context, userID string, id string) error {
	result, err := store.pool.Exec(ctx, `DELETE FROM console_chat_sessions WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanConsoleChatSession(scanner interface{ Scan(dest ...any) error }) (domain.ConsoleChatSession, error) {
	var item domain.ConsoleChatSession
	err := scanner.Scan(&item.UserID, &item.ID, &item.Title, &item.CustomTitle, &item.PublicName, &item.SystemPrompt, &item.Draft, &item.DraftAttachmentsJSON, &item.Status, &item.MessagesJSON, &item.MessageCount, &item.LastRequestID, &item.LastResponseID, &item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.LastMessageAt, &item.CompletedAt)
	return item, err
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

func queryModelCosts(ctx context.Context, pool *pgxpool.Pool) ([]domain.ModelCost, error) {
	rows, err := pool.Query(ctx, `SELECT model_alias, count(*), coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0), coalesce(sum(cost_micro_cents),0) FROM usage_ledger WHERE created_at >= now() - interval '30 days' GROUP BY model_alias ORDER BY 5 DESC LIMIT 8`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.ModelCost
	for rows.Next() {
		var item domain.ModelCost
		if err := rows.Scan(&item.ModelAlias, &item.RequestCount, &item.InputTokens, &item.OutputTokens, &item.CostMicroCents); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func querySpendBreakdowns(ctx context.Context, pool *pgxpool.Pool, statement string) ([]domain.SpendBreakdown, error) {
	rows, err := pool.Query(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.SpendBreakdown
	for rows.Next() {
		var item domain.SpendBreakdown
		var lastSeen time.Time
		if err := rows.Scan(&item.Key, &item.Label, &item.RequestCount, &item.TotalTokens, &item.CostMicroCents, &lastSeen); err != nil {
			return nil, err
		}
		item.LastSeen = &lastSeen
		items = append(items, item)
	}
	return items, rows.Err()
}

func countReadyAuthProviders(settings domain.ConsoleAuthSettings) int {
	count := 0
	for _, provider := range settings.Providers {
		if provider.Enabled && strings.TrimSpace(provider.ClientID) != "" && strings.TrimSpace(provider.ClientSecret) != "" && strings.TrimSpace(provider.AuthURL) != "" && strings.TrimSpace(provider.TokenURL) != "" && strings.TrimSpace(provider.UserInfoURL) != "" {
			count++
		}
	}
	return count
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxTime(current *time.Time, candidate time.Time) *time.Time {
	if candidate.IsZero() {
		return current
	}
	if current == nil || candidate.After(*current) {
		value := candidate
		return &value
	}
	return current
}

func sortConsoleUsers(values []domain.ConsoleUserSummary) {
	sort.Slice(values, func(i, j int) bool {
		left, right := values[i], values[j]
		if left.LastSeen != nil && right.LastSeen != nil && !left.LastSeen.Equal(*right.LastSeen) {
			return left.LastSeen.After(*right.LastSeen)
		}
		if left.RequestCount != right.RequestCount {
			return left.RequestCount > right.RequestCount
		}
		return left.UserID < right.UserID
	})
}
