-- Restores the templates replaced or deleted by 0127 from the backup table.
DELETE FROM notification_email_templates
WHERE id IN (SELECT id FROM notification_template_seed_0127_backup);

INSERT INTO notification_email_templates
SELECT (jsonb_populate_record(NULL::notification_email_templates, row_data)).*
FROM notification_template_seed_0127_backup;

DROP TABLE notification_template_seed_0127_backup;
