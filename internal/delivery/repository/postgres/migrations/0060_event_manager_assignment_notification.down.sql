DELETE FROM event_signal_notification_subscriptions WHERE signal_type = 'event.manager.assigned';
DELETE FROM platform_signal_notification_defaults WHERE signal_type = 'event.manager.assigned';
DELETE FROM notification_in_app_templates WHERE id = '019a0000-0000-7000-8000-000000000060';
