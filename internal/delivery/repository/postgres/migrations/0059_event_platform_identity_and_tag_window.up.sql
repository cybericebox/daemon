-- Preserve the public name already consumed by event pages and notifications;
-- central administration receives its own label.
ALTER TABLE events ADD COLUMN internal_name varchar(255) NOT NULL DEFAULT '';
UPDATE events SET internal_name = name;

-- Existing rows retain their current public schedule. New platform events are
-- explicitly unpublished until an event manager saves a schedule.
ALTER TABLE events ADD COLUMN lifecycle_configured boolean NOT NULL DEFAULT true;
ALTER TABLE events ALTER COLUMN archive_at DROP NOT NULL;

-- A tag may be reused only for disjoint platform availability windows.
-- Empty ranges account for an event archived before its planned opening.
CREATE EXTENSION IF NOT EXISTS btree_gist;
DROP INDEX IF EXISTS event_tag_index;
ALTER TABLE events ADD CONSTRAINT events_tag_window_no_overlap
    EXCLUDE USING gist (
        tag WITH =,
        (CASE WHEN archive_at <= available_from THEN 'empty'::tstzrange
              ELSE tstzrange(available_from, COALESCE(archive_at, 'infinity'::timestamptz), '[)') END) WITH &&
    );
