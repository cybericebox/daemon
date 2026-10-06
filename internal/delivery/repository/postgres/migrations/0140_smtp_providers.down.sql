DROP INDEX mail_smtp_configs_platform_priority_idx;
DROP INDEX mail_smtp_configs_platform_name_idx;
DELETE FROM mail_smtp_configs c
WHERE c.scope_event_id IS NULL
  AND c.id <> (SELECT id FROM mail_smtp_configs WHERE scope_event_id IS NULL ORDER BY priority, created_at LIMIT 1);
CREATE UNIQUE INDEX mail_smtp_configs_platform_idx ON mail_smtp_configs ((true)) WHERE scope_event_id IS NULL;
ALTER TABLE mail_smtp_configs
    DROP COLUMN name, DROP COLUMN priority, DROP COLUMN enabled, DROP COLUMN created_at,
    DROP COLUMN from_name, DROP COLUMN from_address, DROP COLUMN reply_to_name, DROP COLUMN reply_to_address,
    DROP COLUMN usage_day, DROP COLUMN sent_today, DROP COLUMN last_used_at, DROP COLUMN last_error, DROP COLUMN last_error_at;
