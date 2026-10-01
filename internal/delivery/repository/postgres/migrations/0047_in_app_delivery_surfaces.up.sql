-- Preserve the rendered presentation contract at delivery time. Templates can
-- change later; an already-issued banner or inbox entry must not silently
-- change its surface or styling.
ALTER TABLE notification_in_app_templates
    ADD COLUMN dismissible boolean NOT NULL DEFAULT true;

ALTER TABLE in_app_notifications
    ADD COLUMN icon text NOT NULL DEFAULT '',
    ADD COLUMN tone text NOT NULL DEFAULT 'neutral',
    ADD COLUMN accent_color text NOT NULL DEFAULT '',
    ADD COLUMN surface text NOT NULL DEFAULT 'inbox',
    ADD COLUMN auto_dismiss_ms integer,
    ADD COLUMN actions jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN dismissible boolean NOT NULL DEFAULT true,
    ADD COLUMN dismissed_at timestamptz,
    ADD CONSTRAINT in_app_notifications_surface_check CHECK (surface IN ('inbox', 'banner'));

CREATE INDEX in_app_notifications_active_banner_idx
    ON in_app_notifications (user_id, created_at DESC)
    WHERE surface = 'banner' AND dismissed_at IS NULL;
