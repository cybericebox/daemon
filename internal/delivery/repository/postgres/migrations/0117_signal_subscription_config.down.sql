ALTER TABLE event_mail_settings
    ADD COLUMN start_reminder_hours integer NOT NULL DEFAULT 24 CHECK (start_reminder_hours BETWEEN 0 AND 168);
ALTER TABLE event_signal_notification_subscriptions DROP COLUMN config;
ALTER TABLE platform_signal_notification_defaults DROP COLUMN config;
