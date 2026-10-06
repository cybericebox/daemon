DROP INDEX IF EXISTS notification_in_app_templates_event_scope_idx;
DROP INDEX IF EXISTS notification_email_templates_event_scope_idx;
DROP INDEX IF EXISTS uq_inapp_tpl_published;
DROP INDEX IF EXISTS uq_inapp_tpl_draft;
DROP INDEX IF EXISTS uq_email_tpl_published;
DROP INDEX IF EXISTS uq_email_tpl_draft;

CREATE UNIQUE INDEX uq_email_tpl_draft ON notification_email_templates (notification_type) WHERE status = 'draft';
CREATE UNIQUE INDEX uq_email_tpl_published ON notification_email_templates (notification_type) WHERE status = 'published';
CREATE UNIQUE INDEX uq_inapp_tpl_draft ON notification_in_app_templates (notification_type) WHERE status = 'draft';
CREATE UNIQUE INDEX uq_inapp_tpl_published ON notification_in_app_templates (notification_type) WHERE status = 'published';

ALTER TABLE notification_in_app_templates DROP COLUMN IF EXISTS scope_event_id;
ALTER TABLE notification_email_templates DROP COLUMN IF EXISTS scope_event_id;
