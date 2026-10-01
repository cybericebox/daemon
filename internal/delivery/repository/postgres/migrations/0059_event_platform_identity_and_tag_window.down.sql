DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM events WHERE archive_at IS NULL OR lifecycle_configured = false) THEN
        RAISE EXCEPTION 'Cannot roll back event windows while open-ended or unpublished events exist';
    END IF;
END $$;
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_tag_window_no_overlap;
ALTER TABLE events ALTER COLUMN archive_at SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS event_tag_index ON events (tag, archive_at);
ALTER TABLE events DROP COLUMN IF EXISTS lifecycle_configured;
ALTER TABLE events DROP COLUMN IF EXISTS internal_name;
-- btree_gist is intentionally retained: other database objects may use it.
