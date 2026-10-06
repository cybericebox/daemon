DROP INDEX IF EXISTS in_app_notifications_active_banner_idx;

ALTER TABLE in_app_notifications
    DROP CONSTRAINT IF EXISTS in_app_notifications_surface_check,
    DROP COLUMN IF EXISTS dismissed_at,
    DROP COLUMN IF EXISTS dismissible,
    DROP COLUMN IF EXISTS actions,
    DROP COLUMN IF EXISTS auto_dismiss_ms,
    DROP COLUMN IF EXISTS surface,
    DROP COLUMN IF EXISTS accent_color,
    DROP COLUMN IF EXISTS tone,
    DROP COLUMN IF EXISTS icon;

ALTER TABLE notification_in_app_templates
    DROP COLUMN IF EXISTS dismissible;
