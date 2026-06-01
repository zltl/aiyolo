ALTER TABLE cloud_agent_sessions
  ADD COLUMN IF NOT EXISTS shell_state_json text NOT NULL DEFAULT '';