DELETE FROM notification_email_templates WHERE notification_type = 'participant.event.results_published';
DELETE FROM notification_in_app_templates WHERE notification_type = 'participant.event.results_published';
DELETE FROM event_signal_notification_subscriptions WHERE signal_type = 'participant.event.results_published';
DELETE FROM platform_signal_notification_defaults WHERE signal_type = 'participant.event.results_published';

DROP TABLE IF EXISTS inbox_subject_resolutions;

UPDATE in_app_notifications SET resolution = 'resolved' WHERE resolution IN ('expired', 'withdrawn');
ALTER TABLE in_app_notifications
    DROP CONSTRAINT in_app_notifications_resolution_check,
    ADD CONSTRAINT in_app_notifications_resolution_check
        CHECK (resolution IN ('approved', 'rejected', 'fixed', 'resolved'));
