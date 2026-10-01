-- ── email templates: blocks + styling + versioning audit ──
ALTER TABLE notification_email_templates
    ADD COLUMN styling jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN published_at       timestamptz,
    ADD COLUMN updated_by_user_id uuid REFERENCES users (id) ON
DELETE
SET NULL;

-- convert legacy active → published
UPDATE notification_email_templates
SET status = 'published'
WHERE status = 'active';

-- body text → jsonb single rich_text block (raw HTML preserved in one text node)
ALTER TABLE notification_email_templates
    ADD COLUMN body_blocks jsonb NOT NULL DEFAULT '[]';
UPDATE notification_email_templates
SET body_blocks = jsonb_build_array(
        jsonb_build_object(
                'type', 'rich_text',
                'content', jsonb_build_object(
                        'root', jsonb_build_object(
                        'type', 'root',
                        'children', jsonb_build_array(
                                jsonb_build_object(
                                        'type', 'paragraph',
                                        'children', jsonb_build_array(
                                                jsonb_build_object('type', 'text', 'text', COALESCE(body, ''), 'format',
                                                                   0)
                                                    )
                                )
                                    )
                                )
                           )
        )
                  );
ALTER TABLE notification_email_templates DROP COLUMN body;
ALTER TABLE notification_email_templates RENAME COLUMN body_blocks TO body;

-- one draft + one published per type (unpublished may be many)
DROP INDEX IF EXISTS notification_email_templates_type_status_idx;
CREATE UNIQUE INDEX uq_email_tpl_draft
    ON notification_email_templates (notification_type) WHERE status = 'draft';
CREATE UNIQUE INDEX uq_email_tpl_published
    ON notification_email_templates (notification_type) WHERE status = 'published';
CREATE INDEX notification_email_templates_type_status_idx
    ON notification_email_templates (notification_type, status);

-- ── in-app templates: structured fields + versioning audit ──
ALTER TABLE notification_in_app_templates
    ADD COLUMN icon text NOT NULL DEFAULT '',
    ADD COLUMN tone               text        NOT NULL DEFAULT 'neutral',
    ADD COLUMN accent_color       text        NOT NULL DEFAULT '',
    ADD COLUMN surface            text        NOT NULL DEFAULT 'inbox',
    ADD COLUMN auto_dismiss_ms    integer,
    ADD COLUMN actions            jsonb       NOT NULL DEFAULT '[]',
    ADD COLUMN published_at       timestamptz,
    ADD COLUMN updated_by_user_id uuid REFERENCES users (id) ON
DELETE
SET NULL;

UPDATE notification_in_app_templates
SET status = 'published'
WHERE status = 'active';

DROP INDEX IF EXISTS notification_in_app_templates_type_status_idx;
CREATE UNIQUE INDEX uq_inapp_tpl_draft
    ON notification_in_app_templates (notification_type) WHERE status = 'draft';
CREATE UNIQUE INDEX uq_inapp_tpl_published
    ON notification_in_app_templates (notification_type) WHERE status = 'published';
CREATE INDEX notification_in_app_templates_type_status_idx
    ON notification_in_app_templates (notification_type, status);

-- ── reusable email block presets ──
CREATE TABLE notification_email_block_presets
(
    id          uuid PRIMARY KEY,
    name        text        NOT NULL UNIQUE,
    description text        NOT NULL DEFAULT '',
    blocks      jsonb       NOT NULL DEFAULT '[]',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
