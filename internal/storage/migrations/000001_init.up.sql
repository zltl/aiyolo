CREATE TABLE IF NOT EXISTS api_keys (
  id text PRIMARY KEY,
  name text NOT NULL,
  key_hash text NOT NULL UNIQUE,
  prefix text NOT NULL,
  user_id text NOT NULL DEFAULT 'local',
  organization_id text NOT NULL DEFAULT 'default',
  project_id text NOT NULL DEFAULT 'default',
  status text NOT NULL DEFAULT 'active',
  allowed_protocols text[] NOT NULL DEFAULT ARRAY[]::text[],
  allowed_models text[] NOT NULL DEFAULT ARRAY[]::text[],
  rpm_limit integer NOT NULL DEFAULT 0,
  tpm_limit integer NOT NULL DEFAULT 0,
  concurrent_limit integer NOT NULL DEFAULT 0,
  daily_budget_cents bigint NOT NULL DEFAULT 0,
  monthly_budget_cents bigint NOT NULL DEFAULT 0,
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz
);

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS concurrent_limit integer NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS proxy_profiles (
  id text PRIMARY KEY,
  name text NOT NULL,
  type text NOT NULL,
  endpoint text NOT NULL DEFAULT '',
  auth_ciphertext text NOT NULL DEFAULT '',
  region text NOT NULL DEFAULT '',
  timeout_seconds integer NOT NULL DEFAULT 60,
  health_check_url text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'enabled',
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS providers (
  id text PRIMARY KEY,
  name text NOT NULL,
  base_url text NOT NULL,
  protocol text NOT NULL,
  master_key_ciphertext text NOT NULL DEFAULT '',
  default_proxy_id text NOT NULL DEFAULT '',
  priority integer NOT NULL DEFAULT 0,
  weight integer NOT NULL DEFAULT 100,
  status text NOT NULL DEFAULT 'enabled',
  timeout_seconds integer NOT NULL DEFAULT 90,
  rate_limit_hint text NOT NULL DEFAULT '',
  last_health_check timestamptz,
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS model_routes (
  public_name text PRIMARY KEY,
  provider_id text NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  upstream_model text NOT NULL,
  protocol text NOT NULL,
  proxy_profile_id text NOT NULL DEFAULT '',
  price_rule_id text NOT NULL DEFAULT '',
  enabled boolean NOT NULL DEFAULT true,
  priority integer NOT NULL DEFAULT 0,
  weight integer NOT NULL DEFAULT 100,
  context_tokens integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS usage_ledger (
  request_id text PRIMARY KEY,
  user_id text NOT NULL,
  api_key_id text NOT NULL,
  provider_id text NOT NULL,
  model_alias text NOT NULL,
  upstream_model text NOT NULL,
  protocol text NOT NULL,
  endpoint text NOT NULL,
  input_tokens integer NOT NULL DEFAULT 0,
  output_tokens integer NOT NULL DEFAULT 0,
  cache_read_tokens integer NOT NULL DEFAULT 0,
  cache_creation_tokens integer NOT NULL DEFAULT 0,
  total_tokens integer NOT NULL DEFAULT 0,
  cost_micro_cents bigint NOT NULL DEFAULT 0,
  currency text NOT NULL DEFAULT 'USD',
  estimated boolean NOT NULL DEFAULT false,
  stream boolean NOT NULL DEFAULT false,
  status_code integer NOT NULL DEFAULT 0,
  latency_ms bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rate_limit_windows (
  scope text NOT NULL,
  scope_id text NOT NULL,
  window_start timestamptz NOT NULL,
  request_count integer NOT NULL DEFAULT 0,
  token_count integer NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (scope, scope_id, window_start)
);

CREATE TABLE IF NOT EXISTS quota_reservations (
  id text PRIMARY KEY,
  request_id text NOT NULL UNIQUE,
  api_key_id text NOT NULL,
  user_id text NOT NULL DEFAULT '',
  model_alias text NOT NULL DEFAULT '',
  window_start timestamptz NOT NULL,
  estimated_tokens integer NOT NULL DEFAULT 0,
  actual_tokens integer NOT NULL DEFAULT 0,
  estimated_cost_micro_cents bigint NOT NULL DEFAULT 0,
  actual_cost_micro_cents bigint NOT NULL DEFAULT 0,
  status text NOT NULL DEFAULT 'reserved',
  created_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id text PRIMARY KEY,
  request_id text NOT NULL,
  trace_id text NOT NULL DEFAULT '',
  user_id text NOT NULL DEFAULT '',
  api_key_id text NOT NULL DEFAULT '',
  client_ip text NOT NULL DEFAULT '',
  user_agent text NOT NULL DEFAULT '',
  protocol text NOT NULL DEFAULT '',
  endpoint text NOT NULL DEFAULT '',
  model_alias text NOT NULL DEFAULT '',
  provider_id text NOT NULL DEFAULT '',
  upstream_model text NOT NULL DEFAULT '',
  proxy_profile_id text NOT NULL DEFAULT '',
  status_code integer NOT NULL DEFAULT 0,
  error_code text NOT NULL DEFAULT '',
  latency_ms bigint NOT NULL DEFAULT 0,
  input_tokens integer NOT NULL DEFAULT 0,
  output_tokens integer NOT NULL DEFAULT 0,
  cost_micro_cents bigint NOT NULL DEFAULT 0,
  stream boolean NOT NULL DEFAULT false,
  event_type text NOT NULL DEFAULT 'api_call',
  message text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_usage_created_at ON usage_ledger (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_usage_model ON usage_ledger (model_alias, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rate_limit_windows_updated ON rate_limit_windows (updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_quota_reservations_active ON quota_reservations (api_key_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_created_at ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_request_id ON audit_logs (request_id);
