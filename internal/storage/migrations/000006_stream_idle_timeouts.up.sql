ALTER TABLE providers ADD COLUMN IF NOT EXISTS stream_idle_timeout_seconds integer NOT NULL DEFAULT 300;

UPDATE providers
SET stream_idle_timeout_seconds = GREATEST(timeout_seconds, 300)
WHERE stream_idle_timeout_seconds < GREATEST(timeout_seconds, 300);

ALTER TABLE proxy_profiles ADD COLUMN IF NOT EXISTS stream_idle_timeout_seconds integer NOT NULL DEFAULT 300;

UPDATE proxy_profiles
SET stream_idle_timeout_seconds = GREATEST(timeout_seconds, 300)
WHERE stream_idle_timeout_seconds < GREATEST(timeout_seconds, 300);