-- This rollback restores the exact template rows that existed before 0062.
-- It discards edits made to the replacement rows after the migration.
DELETE FROM notification_email_templates;
DELETE FROM notification_in_app_templates;

INSERT INTO notification_email_templates
SELECT (jsonb_populate_record(NULL::notification_email_templates, row_data)).*
FROM notification_template_seed_0062_backup
WHERE channel = 'email';

INSERT INTO notification_in_app_templates
SELECT (jsonb_populate_record(NULL::notification_in_app_templates, row_data)).*
FROM notification_template_seed_0062_backup
WHERE channel = 'in_app';

DROP TABLE notification_template_seed_0062_backup;
