ALTER TABLE console_chat_sessions
  DROP COLUMN IF EXISTS draft_attachments_json,
  DROP COLUMN IF EXISTS draft;