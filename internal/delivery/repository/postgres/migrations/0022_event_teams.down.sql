DROP INDEX IF EXISTS event_participants_team_idx;

ALTER TABLE event_participants
    DROP CONSTRAINT IF EXISTS event_participants_team_role_check,
    DROP COLUMN IF EXISTS team_role,
    DROP COLUMN IF EXISTS team_id;

DROP TABLE IF EXISTS event_teams;

ALTER TABLE event_configs
    DROP CONSTRAINT IF EXISTS event_configs_team_size_check,
    DROP COLUMN IF EXISTS max_teams,
    DROP COLUMN IF EXISTS min_team_size,
    DROP COLUMN IF EXISTS max_team_size;
