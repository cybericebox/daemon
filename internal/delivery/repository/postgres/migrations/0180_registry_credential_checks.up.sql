-- The last result of the periodic check of each registry credential (platform push token, platform pull
-- token, organizer secret of an event): a small JSON document {state, checkedAt, reason, attemptedAt, failure}.
-- It carries reason codes only, never a token. '{}' means never checked.
ALTER TABLE image_registry_settings
    ADD COLUMN push_check jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN pull_check jsonb NOT NULL DEFAULT '{}';

ALTER TABLE event_registry_secrets
    ADD COLUMN credential_check jsonb NOT NULL DEFAULT '{}';
