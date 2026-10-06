-- Mail sender and Reply-To become one model at both levels (platform and
-- Event), separate from the optional SMTP server. Empty field = inherit.
CREATE TABLE mail_identities
(
    id               uuid PRIMARY KEY,
    scope_event_id   uuid UNIQUE REFERENCES events (id) ON DELETE CASCADE,
    from_name        text        NOT NULL DEFAULT '' CHECK (char_length(from_name) <= 64),
    from_address     text        NOT NULL DEFAULT '',
    reply_to_name    text        NOT NULL DEFAULT '' CHECK (char_length(reply_to_name) <= 64),
    reply_to_address text        NOT NULL DEFAULT '',
    updated_by       uuid,
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX mail_identities_platform_idx ON mail_identities ((true)) WHERE scope_event_id IS NULL;

-- Platform sender lived on the platform SMTP row.
INSERT INTO mail_identities (id, scope_event_id, from_name, from_address, reply_to_address, updated_at)
SELECT gen_random_uuid(), NULL, left(from_name, 64), from_address, reply_to, updated_at
FROM mail_smtp_configs
WHERE scope_event_id IS NULL
  AND (from_name <> '' OR from_address <> '' OR reply_to <> '');

-- The Event «contact email» was the same thing as Reply-To shown twice.
INSERT INTO mail_identities (id, scope_event_id, reply_to_address, updated_at)
SELECT gen_random_uuid(), event_id, contact_email, updated_at
FROM event_mail_settings
WHERE contact_email <> '';

ALTER TABLE mail_smtp_configs
    DROP COLUMN from_name,
    DROP COLUMN from_address,
    DROP COLUMN reply_to;
ALTER TABLE event_mail_settings
    DROP COLUMN contact_email;
