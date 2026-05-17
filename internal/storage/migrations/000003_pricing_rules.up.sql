CREATE TABLE IF NOT EXISTS pricing_rules (
  id text PRIMARY KEY,
  model_alias text NOT NULL DEFAULT '',
  provider_id text NOT NULL DEFAULT '' REFERENCES providers(id) ON DELETE RESTRICT,
  currency text NOT NULL DEFAULT 'USD',
  input_price_per_million_tokens bigint NOT NULL DEFAULT 0,
  output_price_per_million_tokens bigint NOT NULL DEFAULT 0,
  cache_read_price_per_million_tokens bigint NOT NULL DEFAULT 0,
  cache_write_price_per_million_tokens bigint NOT NULL DEFAULT 0,
  effective_from timestamptz NOT NULL DEFAULT now(),
  effective_to timestamptz
);

CREATE INDEX IF NOT EXISTS idx_pricing_rules_provider_model ON pricing_rules (provider_id, model_alias);