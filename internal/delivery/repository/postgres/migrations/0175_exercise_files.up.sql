-- Files stored once per task: one row per (task, SHA-256) names the task's canonical file for that content.
-- Bytes are already shared by file_blobs; this row is the per-task identity an upload asks for before it sends
-- the bytes, and the one the draft save rewrites duplicate attachments to. A row goes with its file (the media
-- GC deletes a file once nothing references it) and with its task.
CREATE TABLE exercise_files
(
    exercise_id  uuid        NOT NULL REFERENCES exercises (id) ON DELETE CASCADE,
    content_hash text        NOT NULL,
    file_id      uuid        NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (exercise_id, content_hash)
);

CREATE INDEX exercise_files_file_idx ON exercise_files (file_id);

-- Backfill: the earliest file each task's versions reference per content. Existing versions are not rewritten:
-- duplicates already referenced by published versions or event snapshots stay valid until nothing references them.
INSERT INTO exercise_files
SELECT DISTINCT ON (v.exercise_id, f.content_hash) v.exercise_id, f.content_hash, f.id, f.created_at
FROM file_references r
         JOIN exercise_versions v ON v.id = r.ref_id
         JOIN files f ON f.id = r.file_id
WHERE r.ref_type = 'exercise_version'
ORDER BY v.exercise_id, f.content_hash, f.created_at, f.id;
