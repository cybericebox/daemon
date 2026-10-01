-- users.last_seen was nullable while the sqlc model (and every consumer) treats
-- it as a plain timestamp: CreateUser ... RETURNING * failed scanning the NULL
-- for freshly created users. Align the schema with sessions.last_seen:
-- creation time counts as the first "last seen".
UPDATE users
SET last_seen = created_at
WHERE last_seen IS NULL;

ALTER TABLE users
    ALTER COLUMN last_seen SET DEFAULT now(),
    ALTER COLUMN last_seen SET NOT NULL;
