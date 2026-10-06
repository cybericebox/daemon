CREATE INDEX notification_dispatches_created_id_idx
    ON notification_dispatches (created_at DESC, id DESC);
