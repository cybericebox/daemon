-- A corrected catalog version becomes a new event-local revision. Existing
-- challenges, attempts and lab allocations keep pointing at the old row.
ALTER TABLE event_exercises
    DROP CONSTRAINT event_exercises_event_id_exercise_id_key,
    ADD COLUMN revision integer NOT NULL DEFAULT 1,
    ADD COLUMN status smallint NOT NULL DEFAULT 0 CHECK (status IN (0, 1)),
    ADD COLUMN replaces_event_exercise_id uuid REFERENCES event_exercises (id) ON DELETE RESTRICT,
    ADD COLUMN superseded_at timestamptz;

ALTER TABLE event_exercises
    ADD CONSTRAINT event_exercises_event_exercise_revision_key UNIQUE (event_id, exercise_id, revision);

CREATE UNIQUE INDEX event_exercises_one_active_revision_idx
    ON event_exercises (event_id, exercise_id)
    WHERE status = 0;
