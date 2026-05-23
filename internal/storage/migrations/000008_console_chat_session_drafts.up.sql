ALTER TABLE console_chat_sessions
  ADD COLUMN IF NOT EXISTS draft text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS draft_attachments_json text NOT NULL DEFAULT '[]';