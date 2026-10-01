ALTER TABLE team_challenge_solves
    DROP CONSTRAINT IF EXISTS team_challenge_solves_awarded_points_check,
    DROP COLUMN IF EXISTS awarded_points;
