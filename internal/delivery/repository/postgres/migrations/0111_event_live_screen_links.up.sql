-- Screen links: a projector or LED PC that is not signed in opens the live
-- screen of one event with a random token. Only its SHA-256 is stored.
CREATE TABLE event_live_screen_links
(
    id         uuid PRIMARY KEY,
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    token_hash bytea       NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL,
    created_by uuid        NULL REFERENCES users (id) ON DELETE SET NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    revoked_at timestamptz NULL
);

CREATE INDEX event_live_screen_links_event_idx ON event_live_screen_links (event_id, expires_at);
