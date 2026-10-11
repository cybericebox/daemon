DROP INDEX notification_dispatch_targets_smtp_unclaimed_idx;
ALTER TABLE notification_dispatch_targets
    DROP COLUMN smtp_connection_key,
    DROP COLUMN smtp_provider_id;
