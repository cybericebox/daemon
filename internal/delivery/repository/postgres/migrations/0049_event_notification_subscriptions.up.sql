-- Platform defaults are copied at event creation. Event rows never point back
-- to them, so later platform edits cannot mutate a running event's behaviour.
CREATE TABLE platform_signal_notification_defaults
(
    signal_type text NOT NULL,
    channel text NOT NULL CHECK (channel IN ('email', 'in_app')),
    enabled boolean NOT NULL DEFAULT false,
    audience jsonb NOT NULL DEFAULT '{"kind":"signal_subject"}',
    PRIMARY KEY (signal_type, channel)
);

CREATE TABLE event_signal_notification_subscriptions
(
    scope_event_id uuid NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    signal_type text NOT NULL,
    channel text NOT NULL CHECK (channel IN ('email', 'in_app')),
    enabled boolean NOT NULL DEFAULT false,
    audience jsonb NOT NULL DEFAULT '{"kind":"signal_subject"}',
    PRIMARY KEY (scope_event_id, signal_type, channel)
);

CREATE INDEX event_signal_notification_subscriptions_enabled_idx
    ON event_signal_notification_subscriptions (scope_event_id, signal_type)
    WHERE enabled;
