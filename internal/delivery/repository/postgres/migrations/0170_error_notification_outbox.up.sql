ALTER TABLE error_groups ADD COLUMN notification_enqueued_at timestamptz;
CREATE TABLE error_notification_deliveries (
    id uuid PRIMARY KEY,
    group_id uuid NOT NULL REFERENCES error_groups(id) ON DELETE CASCADE,
    batch_id uuid,
    channel text NOT NULL CHECK (channel IN ('prepare', 'telegram', 'email')),
    target text NOT NULL DEFAULT '',
    label text NOT NULL DEFAULT '',
    body text NOT NULL,
    email_body text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    last_attempt_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    delivered_at timestamptz,
    cancelled_at timestamptz,
    UNIQUE (batch_id, channel, target)
);
CREATE INDEX error_notification_due ON error_notification_deliveries(next_attempt_at) WHERE delivered_at IS NULL AND cancelled_at IS NULL;
CREATE INDEX error_notification_group ON error_notification_deliveries(group_id, created_at DESC);
