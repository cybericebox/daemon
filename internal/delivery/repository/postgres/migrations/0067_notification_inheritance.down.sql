-- Restore per-event copies of the platform-owned manager assignment default.
INSERT INTO event_signal_notification_subscriptions (scope_event_id, signal_type, channel, enabled, audience)
SELECT e.id, d.signal_type, d.channel, d.enabled, d.audience
FROM events e
CROSS JOIN platform_signal_notification_defaults d
WHERE d.signal_type = 'event.manager.assigned'
ON CONFLICT (scope_event_id, signal_type, channel) DO NOTHING;

-- Drop the seeded participant defaults nobody has enabled since the up migration.
DELETE FROM platform_signal_notification_defaults
WHERE enabled = false
  AND signal_type IN ('participant.approval_registration.submitted', 'participant.approval_registration.approved',
                      'participant.approval_registration.rejected', 'participant.open_registration.completed',
                      'participant.invitation.sent', 'participant.invitation.accepted', 'participant.invitation.declined',
                      'participant.invitation.revoked', 'participant.invitation.expired', 'participant.enrolled');
