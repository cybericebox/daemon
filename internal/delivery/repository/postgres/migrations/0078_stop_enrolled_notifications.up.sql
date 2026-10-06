-- participant.enrolled always accompanies a specific action signal that
-- already notifies the participant (one message per action). It no longer has
-- a notification representation, so drop the rows that could enable it.
-- Templates are kept untouched; nothing lists or dispatches them any more.
DELETE FROM event_signal_notification_subscriptions WHERE signal_type = 'participant.enrolled';
DELETE FROM platform_signal_notification_defaults WHERE signal_type = 'participant.enrolled';
