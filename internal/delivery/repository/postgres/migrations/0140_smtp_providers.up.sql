-- Platform SMTP becomes a list of providers: priority order, enabled flag,
-- optional per-provider sender and daily usage counters (UTC day, reset lazily).
-- Event rows (scope_event_id IS NOT NULL) keep one row per Event.
ALTER TABLE mail_smtp_configs
    ADD COLUMN name             text        NOT NULL DEFAULT '' CHECK (char_length(name) <= 64),
    ADD COLUMN priority         integer     NOT NULL DEFAULT 0 CHECK (priority >= 0),
    ADD COLUMN enabled          boolean     NOT NULL DEFAULT true,
    ADD COLUMN created_at       timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN from_name        text        NOT NULL DEFAULT '' CHECK (char_length(from_name) <= 64),
    ADD COLUMN from_address     text        NOT NULL DEFAULT '',
    ADD COLUMN reply_to_name    text        NOT NULL DEFAULT '' CHECK (char_length(reply_to_name) <= 64),
    ADD COLUMN reply_to_address text        NOT NULL DEFAULT '',
    ADD COLUMN usage_day        date,
    ADD COLUMN sent_today       integer     NOT NULL DEFAULT 0 CHECK (sent_today >= 0),
    ADD COLUMN last_used_at     timestamptz,
    ADD COLUMN last_error       text        NOT NULL DEFAULT '',
    ADD COLUMN last_error_at    timestamptz;

-- The existing platform row becomes the first provider (id kept: the password
-- ciphertext is bound to it).
UPDATE mail_smtp_configs SET name = host WHERE scope_event_id IS NULL AND name = '';

DROP INDEX mail_smtp_configs_platform_idx;
CREATE UNIQUE INDEX mail_smtp_configs_platform_name_idx
    ON mail_smtp_configs (lower(name)) WHERE scope_event_id IS NULL;
CREATE INDEX mail_smtp_configs_platform_priority_idx
    ON mail_smtp_configs (priority, created_at) WHERE scope_event_id IS NULL AND enabled;
