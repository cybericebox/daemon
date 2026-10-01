-- Custom broadcasts: one authored message sent to a chosen audience through
-- the regular dispatch pipeline. Every recipient is a dispatch row that points
-- back here, so the delivery journal links to the broadcast and per-recipient
-- failures stay in the journal targets.
CREATE TABLE notification_broadcasts
(
    id               uuid PRIMARY KEY,
    scope_event_id   uuid        REFERENCES events (id) ON DELETE CASCADE,
    created_by       uuid        REFERENCES users (id) ON DELETE SET NULL,
    channels         text[]      NOT NULL,
    subject          text        NOT NULL DEFAULT '',
    preheader        text        NOT NULL DEFAULT '',
    email_body       jsonb       NOT NULL DEFAULT '[]',
    email_styling    jsonb       NOT NULL DEFAULT '{}',
    inapp_title      text        NOT NULL DEFAULT '',
    inapp_body       text        NOT NULL DEFAULT '',
    inapp_link       text        NOT NULL DEFAULT '',
    audience         jsonb       NOT NULL,
    recipient_count  integer     NOT NULL DEFAULT 0 CHECK (recipient_count >= 0),
    status           text        NOT NULL DEFAULT 'sending',
    created_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz,
    CONSTRAINT notification_broadcasts_status_check CHECK (status IN ('sending', 'done', 'failed'))
);

CREATE INDEX notification_broadcasts_created_idx
    ON notification_broadcasts (created_at DESC, id DESC);
CREATE INDEX notification_broadcasts_event_idx
    ON notification_broadcasts (scope_event_id, created_at DESC, id DESC)
    WHERE scope_event_id IS NOT NULL;

ALTER TABLE notification_dispatches
    ADD COLUMN broadcast_id uuid REFERENCES notification_broadcasts (id) ON DELETE CASCADE;

-- One dispatch per recipient per broadcast: a re-run of the sender skips the
-- recipients that were already queued.
CREATE UNIQUE INDEX notification_dispatches_broadcast_recipient_idx
    ON notification_dispatches (broadcast_id, recipient_user_id)
    WHERE broadcast_id IS NOT NULL;
