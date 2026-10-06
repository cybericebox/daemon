DROP INDEX IF EXISTS notification_dispatch_targets_email_done_idx;
ALTER TABLE mail_smtp_configs
    DROP COLUMN IF EXISTS daily_quota,
    DROP COLUMN IF EXISTS max_per_second;
