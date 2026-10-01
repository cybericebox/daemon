CREATE TABLE event_result_revisions
(
    event_id    uuid PRIMARY KEY REFERENCES events (id) ON DELETE CASCADE,
    revision    bigint      NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE event_result_changes
(
    event_id    uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    revision    bigint      NOT NULL CHECK (revision > 0),
    kind        text        NOT NULL,
    payload     jsonb       NOT NULL,
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (event_id, revision)
);

CREATE INDEX event_result_changes_retention_idx
    ON event_result_changes (created_at ASC);
