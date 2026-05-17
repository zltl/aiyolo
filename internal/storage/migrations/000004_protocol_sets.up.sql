ALTER TABLE providers ADD COLUMN IF NOT EXISTS supported_protocols text[] NOT NULL DEFAULT ARRAY[]::text[];

UPDATE providers
SET supported_protocols = CASE
	WHEN protocol IS NULL OR btrim(protocol) = '' THEN ARRAY[]::text[]
	ELSE ARRAY[lower(btrim(protocol))]
END
WHERE supported_protocols = ARRAY[]::text[];

ALTER TABLE model_routes ADD COLUMN IF NOT EXISTS allowed_protocols text[] NOT NULL DEFAULT ARRAY[]::text[];

UPDATE model_routes
SET allowed_protocols = CASE
	WHEN protocol IS NULL OR btrim(protocol) = '' THEN ARRAY[]::text[]
	ELSE ARRAY[lower(btrim(protocol))]
END
WHERE allowed_protocols = ARRAY[]::text[];