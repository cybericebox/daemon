-- Background imports of task archives that carry device images (`image: {file: …}`): one row per archive source
-- (a chunked upload or a stored file), read only by the user who started it. The archive password is not stored
-- here: the River job carries it sealed with the platform cipher.
CREATE TABLE exercise_import_jobs (
    id           uuid        PRIMARY KEY,
    created_by   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source       text        NOT NULL CHECK (source IN ('upload', 'file')),
    source_id    uuid        NOT NULL,
    state        text        NOT NULL CHECK (state IN ('running', 'done', 'failed')),
    exercise_ids uuid[]      NOT NULL DEFAULT '{}',
    error        jsonb       NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    CONSTRAINT exercise_import_jobs_source_key UNIQUE (source_id)
);
