ALTER TABLE event_mail_settings
    ADD COLUMN contact_email text NOT NULL DEFAULT '';
ALTER TABLE mail_smtp_configs
    ADD COLUMN from_name    text NOT NULL DEFAULT '',
    ADD COLUMN from_address text NOT NULL DEFAULT '',
    ADD COLUMN reply_to     text NOT NULL DEFAULT '';

UPDATE event_mail_settings m
SET contact_email = i.reply_to_address
FROM mail_identities i
WHERE i.scope_event_id = m.event_id;

UPDATE mail_smtp_configs c
SET from_name = i.from_name, from_address = i.from_address, reply_to = i.reply_to_address
FROM mail_identities i
WHERE c.scope_event_id IS NULL AND i.scope_event_id IS NULL;

DROP TABLE mail_identities;
