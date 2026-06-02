ALTER TABLE cloud_agent_accounts
  ALTER COLUMN agent_type SET DEFAULT 'codex';

ALTER TABLE cloud_agent_sessions
  ALTER COLUMN agent_type SET DEFAULT 'codex';

UPDATE cloud_agent_accounts
  SET agent_type = 'codex'
  WHERE agent_type = 'claude-code';

UPDATE cloud_agent_sessions
  SET agent_type = 'codex'
  WHERE agent_type = 'claude-code';