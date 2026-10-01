-- A manager assignment is a durable event-scoped signal. Inbox delivery is
-- enabled by default for the assignee; email remains an opt-in channel.
INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
VALUES ('event.manager.assigned', 'in_app', true, '{"kind":"signal_subject"}'::jsonb)
ON CONFLICT (signal_type, channel) DO NOTHING;

INSERT INTO event_signal_notification_subscriptions (scope_event_id, signal_type, channel, enabled, audience)
SELECT id, 'event.manager.assigned', 'in_app', true, '{"kind":"signal_subject"}'::jsonb
FROM events
ON CONFLICT (scope_event_id, signal_type, channel) DO NOTHING;

INSERT INTO notification_in_app_templates
    (id, notification_type, status, title, body, link, published_at, surface, dismissible)
VALUES
    ('019a0000-0000-7000-8000-000000000060', 'event.manager.assigned', 'published',
     'Доступ до заходу', 'Вас призначено {{.role_name}} заходу «{{.event_name}}».', '', now(), 'inbox', true)
ON CONFLICT (id) DO NOTHING;
