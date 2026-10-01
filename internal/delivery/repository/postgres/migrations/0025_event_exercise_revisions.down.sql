DROP INDEX IF EXISTS event_exercises_one_active_revision_idx;
ALTER TABLE event_exercises
    DROP CONSTRAINT IF EXISTS event_exercises_event_exercise_revision_key,
    DROP COLUMN IF EXISTS superseded_at,
    DROP COLUMN IF EXISTS replaces_event_exercise_id,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS revision,
    ADD CONSTRAINT event_exercises_event_id_exercise_id_key UNIQUE (event_id, exercise_id);
