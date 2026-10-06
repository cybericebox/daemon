-- A template may be the platform default (scope_event_id IS NULL) or the
-- frozen event-specific copy. The lookup path always prefers the latter.
ALTER TABLE notification_email_templates
    ADD COLUMN scope_event_id uuid REFERENCES events (id) ON DELETE CASCADE;
ALTER TABLE notification_in_app_templates
    ADD COLUMN scope_event_id uuid REFERENCES events (id) ON DELETE CASCADE;

DROP INDEX IF EXISTS uq_email_tpl_draft;
DROP INDEX IF EXISTS uq_email_tpl_published;
DROP INDEX IF EXISTS uq_inapp_tpl_draft;
DROP INDEX IF EXISTS uq_inapp_tpl_published;

CREATE UNIQUE INDEX uq_email_tpl_draft
    ON notification_email_templates (notification_type, COALESCE(scope_event_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE status = 'draft';
CREATE UNIQUE INDEX uq_email_tpl_published
    ON notification_email_templates (notification_type, COALESCE(scope_event_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE status = 'published';
CREATE UNIQUE INDEX uq_inapp_tpl_draft
    ON notification_in_app_templates (notification_type, COALESCE(scope_event_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE status = 'draft';
CREATE UNIQUE INDEX uq_inapp_tpl_published
    ON notification_in_app_templates (notification_type, COALESCE(scope_event_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE status = 'published';

CREATE INDEX notification_email_templates_event_scope_idx
    ON notification_email_templates (scope_event_id, notification_type, status);
CREATE INDEX notification_in_app_templates_event_scope_idx
    ON notification_in_app_templates (scope_event_id, notification_type, status);
