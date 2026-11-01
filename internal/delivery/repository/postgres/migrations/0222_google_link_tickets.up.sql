-- A Google identity that signed in with the verified address of an existing account is not linked at once (except
-- Gmail): it waits here for the owner's confirmation (the password or a code mailed to the address). One short-lived,
-- single-use ticket per (account, Google identity); only the hash of the ticket and of the mailed code is stored.
CREATE TABLE google_link_tickets
(
    id            uuid PRIMARY KEY,
    ticket_hash   text        NOT NULL UNIQUE,
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    google_id     text        NOT NULL,
    google_email  text        NOT NULL DEFAULT '',
    google_name   text        NOT NULL DEFAULT '',
    return_to     text        NOT NULL DEFAULT '',
    code_hash     text        NOT NULL DEFAULT '',
    code_sent_at  timestamptz,
    code_attempts integer     NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    UNIQUE (user_id, google_id)
);
CREATE INDEX google_link_tickets_expires_idx ON google_link_tickets (expires_at);
