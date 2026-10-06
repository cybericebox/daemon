CREATE TABLE challenge_attempt_decisions
(
    id                   uuid        PRIMARY KEY,
    challenge_attempt_id uuid        NOT NULL REFERENCES challenge_attempts (id) ON DELETE CASCADE,
    decision             smallint    NOT NULL CHECK (decision IN (0, 1, 2)),
    reason               text        NOT NULL CHECK (length(btrim(reason)) > 0),
    decided_by           uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    decided_at           timestamptz NOT NULL
);

CREATE INDEX challenge_attempt_decisions_latest_idx
    ON challenge_attempt_decisions (challenge_attempt_id, decided_at DESC, id DESC);
