ALTER TABLE team_challenges ADD COLUMN solved_at timestamptz;
DROP INDEX IF EXISTS team_challenge_solves_time_idx;
DROP TABLE IF EXISTS team_challenge_solves;
DROP VIEW IF EXISTS effective_challenge_attempts;
