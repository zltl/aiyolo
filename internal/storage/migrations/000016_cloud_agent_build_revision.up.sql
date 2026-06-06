ALTER TABLE cloud_agent_accounts
  ADD COLUMN IF NOT EXISTS last_build_revision TEXT NOT NULL DEFAULT '';
