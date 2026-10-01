-- Event rows become overrides only. Manager assignment is platform-owned.
DELETE FROM event_signal_notification_subscriptions WHERE signal_type = 'event.manager.assigned';

-- Every Event-scoped (signal, channel) gets a platform default so inheritance
-- always has a base row. Disabled by default: participants get nothing until
-- the platform or an Event enables it (owner decision 2026-09-26).
INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
SELECT s.signal_type, c.channel, false, '{"kind":"signal_subject"}'::jsonb
FROM (VALUES ('participant.approval_registration.submitted'), ('participant.approval_registration.approved'),
             ('participant.approval_registration.rejected'), ('participant.open_registration.completed'),
             ('participant.invitation.sent'), ('participant.invitation.accepted'), ('participant.invitation.declined'),
             ('participant.invitation.revoked'), ('participant.invitation.expired'), ('participant.enrolled')) AS s(signal_type)
CROSS JOIN (VALUES ('email'), ('in_app')) AS c(channel)
ON CONFLICT (signal_type, channel) DO NOTHING;
