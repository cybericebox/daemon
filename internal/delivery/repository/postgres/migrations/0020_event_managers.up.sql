CREATE TABLE event_managers
(
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       smallint    NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (event_id, user_id),
    CONSTRAINT event_managers_role_check CHECK (role IN (0, 1, 2))
);

CREATE INDEX event_managers_user_id_idx ON event_managers (user_id, event_id);
