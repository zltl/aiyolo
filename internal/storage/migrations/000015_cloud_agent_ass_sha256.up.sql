ALTER TABLE cloud_agent_accounts
  ADD COLUMN IF NOT EXISTS last_ass_sha256 TEXT NOT NULL DEFAULT '';
