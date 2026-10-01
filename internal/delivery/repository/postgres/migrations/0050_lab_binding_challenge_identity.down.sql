ALTER TABLE lab_bindings
    ADD COLUMN event_exercise_id uuid;

UPDATE lab_bindings lb
SET event_exercise_id = ec.event_exercise_id
FROM event_challenges ec
WHERE ec.id = lb.event_challenge_id;

ALTER TABLE lab_bindings
    ALTER COLUMN event_exercise_id SET NOT NULL,
    ADD CONSTRAINT lab_bindings_event_exercise_id_fkey
        FOREIGN KEY (event_exercise_id) REFERENCES event_exercises (id) ON DELETE RESTRICT,
    DROP CONSTRAINT IF EXISTS lab_bindings_event_team_id_event_challenge_id_key,
    ADD CONSTRAINT lab_bindings_event_team_id_event_exercise_id_key
        UNIQUE (event_team_id, event_exercise_id),
    DROP CONSTRAINT IF EXISTS lab_bindings_event_challenge_id_fkey,
    DROP COLUMN event_challenge_id,
    DROP CONSTRAINT IF EXISTS lab_bindings_readiness_check,
    ADD CONSTRAINT lab_bindings_readiness_check CHECK (readiness IN (0, 1, 2));
