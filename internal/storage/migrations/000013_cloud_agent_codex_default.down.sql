ALTER TABLE cloud_agent_accounts
  ALTER COLUMN agent_type SET DEFAULT 'claude-code';

ALTER TABLE cloud_agent_sessions
  ALTER COLUMN agent_type SET DEFAULT 'claude-code';