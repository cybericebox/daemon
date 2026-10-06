-- Fixed scoring modes must retain the value that was awarded at the moment a
-- team first solved a task. Popularity scoring deliberately leaves this NULL
-- and is calculated from the current projection instead.
ALTER TABLE team_challenge_solves
    ADD COLUMN awarded_points integer;

ALTER TABLE team_challenge_solves
    ADD CONSTRAINT team_challenge_solves_awarded_points_check
        CHECK (awarded_points IS NULL OR awarded_points > 0);

-- Existing competitions used static event_challenge points. Preserve that
-- behaviour for their already-materialized solved projection.
UPDATE team_challenge_solves solved
SET awarded_points = challenge.points
FROM team_challenges team_challenge
JOIN event_challenges challenge ON challenge.id = team_challenge.event_challenge_id
WHERE team_challenge.id = solved.team_challenge_id;
