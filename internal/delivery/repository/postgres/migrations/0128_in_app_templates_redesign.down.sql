-- Restores the in-app templates replaced by 0128 from the backup table.
DELETE FROM notification_in_app_templates
WHERE id IN (SELECT id FROM notification_template_seed_0128_backup);

INSERT INTO notification_in_app_templates
SELECT (jsonb_populate_record(NULL::notification_in_app_templates, row_data)).*
FROM notification_template_seed_0128_backup;

DROP TABLE notification_template_seed_0128_backup;
