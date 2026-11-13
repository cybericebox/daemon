-- State of the re-encryption of stored secrets, one row per source (a table column) of a key family. Counts only: no
-- value, no ciphertext. `keys status` reads it; the job writes it after every batch.
CREATE TABLE key_rotation_progress (
    family       text        NOT NULL,
    source       text        NOT NULL,
    key_id       text        NOT NULL,
    state        text        NOT NULL CHECK (state IN ('running', 'finished', 'finished_with_failures', 'interrupted', 'failed')),
    total        integer     NOT NULL DEFAULT 0,
    done         integer     NOT NULL DEFAULT 0,
    failed       integer     NOT NULL DEFAULT 0,
    conflicts    integer     NOT NULL DEFAULT 0,
    failed_by_reason jsonb   NOT NULL DEFAULT '{}'::jsonb,
    started_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (family, source)
);
