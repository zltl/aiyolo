CREATE TABLE IF NOT EXISTS codex_install_tokens (
  id text PRIMARY KEY,
  token_hash text NOT NULL UNIQUE,
  created_by text NOT NULL DEFAULT '',
  platform text NOT NULL DEFAULT 'windows',
  default_model text NOT NULL DEFAULT '',
  allowed_models text[] NOT NULL DEFAULT ARRAY[]::text[],
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  used_at timestamptz,
  api_key_id text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS codex_install_tokens_expires_at_idx ON codex_install_tokens (expires_at);