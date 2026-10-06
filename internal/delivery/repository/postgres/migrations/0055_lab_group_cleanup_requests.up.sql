CREATE TABLE lab_group_cleanup_requests
(
    lab_group_name text        PRIMARY KEY,
    event_id       uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    requested_at   timestamptz NOT NULL,
    destroyed_at   timestamptz
);

CREATE INDEX lab_group_cleanup_requests_pending_idx
    ON lab_group_cleanup_requests (requested_at, lab_group_name)
    WHERE destroyed_at IS NULL;
