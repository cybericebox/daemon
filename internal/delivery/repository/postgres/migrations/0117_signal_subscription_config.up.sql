-- Per-signal options that belong to the notification, not to the Event mail
-- settings. The start reminder keeps its timing here: days_before_start.
ALTER TABLE platform_signal_notification_defaults ADD COLUMN config jsonb NOT NULL DEFAULT '{}';
ALTER TABLE event_signal_notification_subscriptions ADD COLUMN config jsonb NOT NULL DEFAULT '{}';

UPDATE platform_signal_notification_defaults
SET config = '{"days_before_start":7}'
WHERE signal_type = 'participant.event.start_reminder';

-- Events that saved a non-default reminder keep it: hours become whole days
-- (rounded up, at least 1); 0 hours meant "no reminder", so both channels of
-- the reminder are switched off for that Event.
INSERT INTO event_signal_notification_subscriptions (scope_event_id, signal_type, channel, enabled, audience)
SELECT m.event_id, d.signal_type, d.channel, d.enabled, d.audience
FROM event_mail_settings m
CROSS JOIN platform_signal_notification_defaults d
WHERE d.signal_type = 'participant.event.start_reminder' AND m.start_reminder_hours <> 24
ON CONFLICT (scope_event_id, signal_type, channel) DO NOTHING;

UPDATE event_signal_notification_subscriptions s
SET enabled = false
FROM event_mail_settings m
WHERE s.scope_event_id = m.event_id AND s.signal_type = 'participant.event.start_reminder'
  AND m.start_reminder_hours = 0;

UPDATE event_signal_notification_subscriptions s
SET config = jsonb_build_object('days_before_start', GREATEST(1, CEIL(m.start_reminder_hours / 24.0)::int))
FROM event_mail_settings m
WHERE s.scope_event_id = m.event_id AND s.signal_type = 'participant.event.start_reminder'
  AND s.channel = 'email' AND m.start_reminder_hours > 0 AND m.start_reminder_hours <> 24;

ALTER TABLE event_mail_settings DROP COLUMN start_reminder_hours;
