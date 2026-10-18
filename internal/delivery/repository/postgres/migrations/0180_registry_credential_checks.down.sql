ALTER TABLE event_registry_secrets
    DROP COLUMN IF EXISTS credential_check;

ALTER TABLE image_registry_settings
    DROP COLUMN IF EXISTS pull_check,
    DROP COLUMN IF EXISTS push_check;
