DROP TABLE IF EXISTS notification_email_block_presets;

-- in-app: drop versioning indexes + structured columns; collapse published→active
DROP INDEX IF EXISTS uq_inapp_tpl_draft;
DROP INDEX IF EXISTS uq_inapp_tpl_published;
UPDATE notification_in_app_templates
SET status = 'active'
WHERE status IN ('published', 'unpublished');
ALTER TABLE notification_in_app_templates
DROP
COLUMN icon, DROP
COLUMN tone, DROP
COLUMN accent_color, DROP COLUMN surface,
    DROP
COLUMN auto_dismiss_ms, DROP
COLUMN actions, DROP
COLUMN published_at, DROP COLUMN updated_by_user_id;
DROP INDEX IF EXISTS notification_in_app_templates_type_status_idx;
CREATE INDEX notification_in_app_templates_type_status_idx
    ON notification_in_app_templates (notification_type, status);

-- email: body jsonb → text (best-effort: concatenate top-level rich_text text nodes)
DROP INDEX IF EXISTS uq_email_tpl_draft;
DROP INDEX IF EXISTS uq_email_tpl_published;
ALTER TABLE notification_email_templates
    ADD COLUMN body_text text NOT NULL DEFAULT '';
UPDATE notification_email_templates
SET body_text = COALESCE((SELECT string_agg(node ->> 'text', '')
                          FROM jsonb_array_elements(body) blk,
                               jsonb_array_elements(blk - > 'content' - > 'root' - > 'children') para,
                               jsonb_array_elements(para - > 'children') node
                          WHERE blk ->> 'type' = 'rich_text' AND node ->> 'type' = 'text'
), '');
ALTER TABLE notification_email_templates DROP COLUMN body;
ALTER TABLE notification_email_templates RENAME COLUMN body_text TO body;
UPDATE notification_email_templates
SET status = 'active'
WHERE status IN ('published', 'unpublished');
ALTER TABLE notification_email_templates
DROP
COLUMN styling, DROP
COLUMN published_at, DROP
COLUMN updated_by_user_id;
DROP INDEX IF EXISTS notification_email_templates_type_status_idx;
CREATE INDEX notification_email_templates_type_status_idx
    ON notification_email_templates (notification_type, status);
