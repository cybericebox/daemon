-- Restore the disabled platform defaults seeded by 0067. Event overrides that
-- existed before the up migration are not recoverable.
INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
SELECT 'participant.enrolled', c.channel, false, '{"kind":"signal_subject"}'::jsonb
FROM (VALUES ('email'), ('in_app')) AS c(channel)
ON CONFLICT (signal_type, channel) DO NOTHING;
