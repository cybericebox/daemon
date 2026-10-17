-- Background exports of tasks «з образами» (the archive carries the platform images of their devices, up to 10 GiB):
-- one row per export, read only by the user who started it. The export options (secrets, the sealed archive
-- password) are not stored here: the River job carries them. file_id is the stored zip once the export is done; the
-- media cleanup removes the file after its grace window, so there is no foreign key.
CREATE TABLE exercise_export_jobs (
    id           uuid        PRIMARY KEY,
    created_by   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    exercise_ids uuid[]      NOT NULL DEFAULT '{}',
    file_name    text        NOT NULL,
    state        text        NOT NULL CHECK (state IN ('running', 'done', 'failed')),
    file_id      uuid        NULL,
    error        jsonb       NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);
