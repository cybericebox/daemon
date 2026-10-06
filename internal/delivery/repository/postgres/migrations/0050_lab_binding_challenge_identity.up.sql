ALTER TABLE lab_bindings
    ADD COLUMN event_challenge_id uuid;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM lab_bindings lb
        CROSS JOIN LATERAL (
            SELECT count(*) AS challenge_count
            FROM event_challenges ec
            WHERE ec.event_exercise_id = lb.event_exercise_id
        ) candidates
        WHERE candidates.challenge_count <> 1
    ) THEN
        RAISE EXCEPTION 'cannot migrate lab binding: its exercise must have exactly one event challenge';
    END IF;
END $$;

UPDATE lab_bindings lb
SET event_challenge_id = ec.id
FROM event_challenges ec
WHERE ec.event_exercise_id = lb.event_exercise_id;

ALTER TABLE lab_bindings
    ALTER COLUMN event_challenge_id SET NOT NULL,
    ADD CONSTRAINT lab_bindings_event_challenge_id_fkey
        FOREIGN KEY (event_challenge_id) REFERENCES event_challenges (id) ON DELETE RESTRICT,
    DROP CONSTRAINT IF EXISTS lab_bindings_event_team_id_event_exercise_id_key,
    ADD CONSTRAINT lab_bindings_event_team_id_event_challenge_id_key
        UNIQUE (event_team_id, event_challenge_id),
    DROP CONSTRAINT IF EXISTS lab_bindings_event_exercise_id_fkey,
    DROP COLUMN event_exercise_id,
    DROP CONSTRAINT IF EXISTS lab_bindings_readiness_check,
    ADD CONSTRAINT lab_bindings_readiness_check CHECK (readiness IN (0, 1, 2, 3));
