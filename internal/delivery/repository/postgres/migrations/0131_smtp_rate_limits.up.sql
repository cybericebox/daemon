-- Per-transport send limits. NULL = not set here (the env value, then no limit).
-- The table holds the platform row and one row per Event, so each Event
-- transport carries its own limits.
ALTER TABLE mail_smtp_configs
    ADD COLUMN max_per_second double precision CHECK (max_per_second > 0),
    ADD COLUMN daily_quota    integer CHECK (daily_quota > 0);

-- Rolling 24 h count of delivered email per transport.
CREATE INDEX notification_dispatch_targets_email_done_idx
    ON notification_dispatch_targets (transport, updated_at DESC)
    WHERE channel = 'email' AND status = 'done';
