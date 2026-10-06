-- Files attached to participant or team answers («Файл» questions). A row is
-- created on upload and holds the file for its uploader until an answer is
-- saved; then it belongs to the participant or the team (owner_id). The blob
-- itself is a media file kept alive by a file_references row
-- (ref_type 'event_answer_file', ref_id = file_id); retention removes both.
CREATE TABLE IF NOT EXISTS event_answer_files
(
    file_id      uuid PRIMARY KEY REFERENCES files (id) ON DELETE CASCADE,
    event_id     uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    scope        text        NOT NULL CHECK (scope IN ('participant', 'team')),
    field_key    text        NOT NULL,
    name         text        NOT NULL,
    size_bytes   bigint      NOT NULL,
    content_type text        NOT NULL,
    uploaded_by  uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    owner_id     uuid,
    attached_at  timestamptz,
    created_at   timestamptz NOT NULL,
    CHECK ((owner_id IS NULL) = (attached_at IS NULL))
);

CREATE INDEX IF NOT EXISTS event_answer_files_owner_idx ON event_answer_files (event_id, scope, owner_id);
CREATE INDEX IF NOT EXISTS event_answer_files_pending_idx ON event_answer_files (created_at) WHERE attached_at IS NULL;
