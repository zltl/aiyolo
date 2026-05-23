CREATE TABLE IF NOT EXISTS console_chat_sessions (
  user_id text NOT NULL,
  id text NOT NULL,
  title text NOT NULL DEFAULT '',
  custom_title boolean NOT NULL DEFAULT false,
  public_name text NOT NULL DEFAULT '',
  system_prompt text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'ready',
  messages_json text NOT NULL DEFAULT '[]',
  message_count integer NOT NULL DEFAULT 0,
  last_request_id text NOT NULL DEFAULT '',
  last_response_id text NOT NULL DEFAULT '',
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  last_message_at timestamptz,
  completed_at timestamptz,
  PRIMARY KEY (user_id, id)
);

CREATE INDEX IF NOT EXISTS idx_console_chat_sessions_user_updated
  ON console_chat_sessions (user_id, updated_at DESC);