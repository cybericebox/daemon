CREATE TABLE request_idempotency
(
    owner_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope          text        NOT NULL,
    key            uuid        NOT NULL,
    request_hash   bytea       NOT NULL CHECK (octet_length(request_hash) = 32),
    completed      boolean     NOT NULL DEFAULT FALSE,
    response_status integer,
    response_body  jsonb,
    created_at     timestamptz NOT NULL,
    expires_at     timestamptz NOT NULL,
    PRIMARY KEY (owner_id, scope, key),
    CHECK ((completed AND response_status IS NOT NULL AND response_body IS NOT NULL) OR
           (NOT completed AND response_status IS NULL AND response_body IS NULL))
);

CREATE INDEX request_idempotency_expiry_idx
    ON request_idempotency (expires_at ASC)
    WHERE completed;
