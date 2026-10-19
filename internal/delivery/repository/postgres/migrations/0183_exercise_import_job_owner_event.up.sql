-- The event a background import creates its tasks in (an event moderator imports into an event they manage); NULL
-- imports into the platform catalog. The event going away cancels the import with it.
ALTER TABLE exercise_import_jobs
    ADD COLUMN owner_event_id uuid NULL REFERENCES events (id) ON DELETE CASCADE;
