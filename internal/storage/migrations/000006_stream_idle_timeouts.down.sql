ALTER TABLE proxy_profiles DROP COLUMN IF EXISTS stream_idle_timeout_seconds;

ALTER TABLE providers DROP COLUMN IF EXISTS stream_idle_timeout_seconds;