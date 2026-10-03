-- Sessions end through a revocation list instead of a per-request session read: the cookie carries the session
-- (encrypted), every replica keeps the revoked session ids in memory and polls this table once a second.
-- seq orders the rows; revoked_at is the database time (the poll reads WHERE revoked_at > watermark - 10 s, the
-- overlap covers transactions that commit late, the session id dedupes); expires_at is when the cookie would die
-- by itself (the smaller of sign-in + absolute TTL and last_seen + idle TTL + 1 min margin), after which the
-- cleanup worker drops the row. No foreign key to users: a row must outlive a hard-deleted user until it expires.
CREATE TABLE session_revocations
(
    seq        bigserial PRIMARY KEY,
    session_id uuid        NOT NULL,
    user_id    uuid        NOT NULL,
    revoked_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX session_revocations_revoked_at_idx ON session_revocations (revoked_at);
CREATE INDEX session_revocations_expires_at_idx ON session_revocations (expires_at);

-- The session cookie changes from an HMAC-signed JWT to an encrypted ticket. Every old cookie stops working (no
-- compatibility shim: users sign in again), so the rows of those sessions would only show as phantom devices.
DELETE FROM sessions;

-- Resumable chunked uploads through the API (a request body is limited to 100 MB at the edge). One row per
-- upload in progress: the chunks are stored as temporary objects (uploads/<id>/<n>) and assembled, hashed and
-- size-checked when the last one arrives. Chunks arrive in order; chunks_received is where a resumed upload
-- continues. The cleanup of the media GC drops uploads past expires_at together with their chunk objects.
CREATE TABLE media_uploads
(
    id              uuid PRIMARY KEY,
    created_by      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name            text        NOT NULL,
    content_type    text        NOT NULL,
    size_bytes      bigint      NOT NULL CHECK (size_bytes > 0),
    chunk_bytes     bigint      NOT NULL CHECK (chunk_bytes > 0),
    chunks_received integer     NOT NULL DEFAULT 0 CHECK (chunks_received >= 0),
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL
);
CREATE INDEX media_uploads_created_by_idx ON media_uploads (created_by);
CREATE INDEX media_uploads_expires_at_idx ON media_uploads (expires_at);
