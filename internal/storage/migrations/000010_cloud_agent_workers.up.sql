CREATE TABLE IF NOT EXISTS worker_ssh_keys (
  id text PRIMARY KEY,
  name text NOT NULL DEFAULT '',
  username text NOT NULL DEFAULT '',
  public_key text NOT NULL DEFAULT '',
  private_key_ciphertext text NOT NULL DEFAULT '',
  passphrase_ciphertext text NOT NULL DEFAULT '',
  fingerprint text NOT NULL DEFAULT '',
  comment text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS worker_servers (
  id text PRIMARY KEY,
  name text NOT NULL DEFAULT '',
  expected_ubuntu_version text NOT NULL DEFAULT '26.04',
  ssh_host text NOT NULL DEFAULT '',
  ssh_port integer NOT NULL DEFAULT 22,
  ssh_username text NOT NULL DEFAULT '',
  ssh_key_id text NOT NULL DEFAULT '',
  install_proxy_id text NOT NULL DEFAULT 'direct',
  labels text[] NOT NULL DEFAULT '{}',
  data_root text NOT NULL DEFAULT '/var/lib/aiyolo-agent',
  status text NOT NULL DEFAULT 'pending',
  last_probe_status text NOT NULL DEFAULT 'unknown',
  last_probe_error text NOT NULL DEFAULT '',
  last_probe_summary_json text NOT NULL DEFAULT '',
  last_probed_at timestamptz,
  last_init_job_id text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_worker_servers_updated
  ON worker_servers (updated_at DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS worker_data_disks (
  worker_id text NOT NULL REFERENCES worker_servers(id) ON DELETE CASCADE,
  device_path text NOT NULL,
  mount_path text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (worker_id, device_path, mount_path)
);

CREATE TABLE IF NOT EXISTS worker_init_jobs (
  worker_id text NOT NULL REFERENCES worker_servers(id) ON DELETE CASCADE,
  id text NOT NULL,
  action text NOT NULL DEFAULT 'bootstrap',
  status text NOT NULL DEFAULT 'queued',
  triggered_by text NOT NULL DEFAULT '',
  log_summary text NOT NULL DEFAULT '',
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  completed_at timestamptz,
  PRIMARY KEY (worker_id, id)
);

CREATE INDEX IF NOT EXISTS idx_worker_init_jobs_worker_updated
  ON worker_init_jobs (worker_id, updated_at DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS worker_init_job_events (
  worker_id text NOT NULL,
  job_id text NOT NULL,
  sequence bigint NOT NULL,
  level text NOT NULL DEFAULT 'info',
  message text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (worker_id, job_id, sequence),
  FOREIGN KEY (worker_id, job_id) REFERENCES worker_init_jobs(worker_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_worker_init_job_events_created
  ON worker_init_job_events (worker_id, job_id, created_at ASC);

CREATE TABLE IF NOT EXISTS cloud_agent_accounts (
  user_id text NOT NULL,
  id text NOT NULL,
  worker_id text NOT NULL REFERENCES worker_servers(id) ON DELETE CASCADE,
  agent_type text NOT NULL DEFAULT 'claude-code',
  model_public_name text NOT NULL DEFAULT '',
  container_id text NOT NULL DEFAULT '',
  container_name text NOT NULL DEFAULT '',
  workspace_path text NOT NULL DEFAULT '/workspace',
  credential_ciphertext text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'stopped',
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  last_started_at timestamptz,
  last_seen_at timestamptz,
  PRIMARY KEY (user_id, id)
);

CREATE INDEX IF NOT EXISTS idx_cloud_agent_accounts_user_worker
  ON cloud_agent_accounts (user_id, worker_id, updated_at DESC, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_cloud_agent_accounts_tuple
  ON cloud_agent_accounts (user_id, worker_id, agent_type);

CREATE TABLE IF NOT EXISTS cloud_agent_sessions (
  user_id text NOT NULL,
  id text NOT NULL,
  worker_id text NOT NULL REFERENCES worker_servers(id) ON DELETE CASCADE,
  account_id text NOT NULL,
  agent_type text NOT NULL DEFAULT 'claude-code',
  chat_session_id text NOT NULL DEFAULT '',
  workspace_path text NOT NULL DEFAULT '/workspace',
  status text NOT NULL DEFAULT 'pending',
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  closed_at timestamptz,
  PRIMARY KEY (user_id, id),
  FOREIGN KEY (user_id, account_id) REFERENCES cloud_agent_accounts(user_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_cloud_agent_sessions_user_worker
  ON cloud_agent_sessions (user_id, worker_id, updated_at DESC, created_at DESC);