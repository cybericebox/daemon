-- Preserve pre-save test usage without retaining SMTP credentials.
-- No provider FK: deleting a provider must not make its tests claimable again.
ALTER TABLE notification_dispatch_targets
    ADD COLUMN smtp_connection_key text NOT NULL DEFAULT '',
    ADD COLUMN smtp_provider_id uuid;
CREATE INDEX notification_dispatch_targets_smtp_unclaimed_idx
    ON notification_dispatch_targets (smtp_connection_key)
    WHERE channel = 'email' AND status = 'done' AND smtp_provider_id IS NULL AND smtp_connection_key <> '';
