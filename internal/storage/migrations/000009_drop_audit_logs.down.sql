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

CREATE INDEX IF NOT EXISTS idx_audit_created_at ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_request_id ON audit_logs (request_id);