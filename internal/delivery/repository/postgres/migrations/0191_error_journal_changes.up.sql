-- The change log behind the live stream of the error journal. Every replica that has an open stream reads the
-- rows after its cursor (seq), so an error recorded on one replica reaches the streams of the others without
-- LISTEN/NOTIFY. Rows are written last in the capture transaction under one advisory lock, so seq order is commit
-- order and a cursor never skips a row. Rows older than a day are purged.
CREATE TABLE error_journal_changes
(
    seq       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id  uuid        NOT NULL REFERENCES error_groups (id) ON DELETE CASCADE,
    sample_id uuid,
    is_new    boolean     NOT NULL DEFAULT false,
    origin    text        NOT NULL DEFAULT '',
    at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX error_journal_changes_at_idx ON error_journal_changes (at);

COMMENT ON TABLE error_journal_changes IS 'Change log of the error journal for the live stream: one row per recorded occurrence or status change; origin is the replica that wrote it (it already showed the event to its own streams)';
