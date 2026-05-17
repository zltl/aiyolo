CREATE TABLE IF NOT EXISTS console_settings (
  key text PRIMARY KEY,
  value_ciphertext text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now()
);