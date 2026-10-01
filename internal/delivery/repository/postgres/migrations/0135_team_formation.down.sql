DROP FUNCTION IF EXISTS lab_group_name(uuid, uuid);
DROP FUNCTION IF EXISTS lab_short_id(uuid);
DROP FUNCTION IF EXISTS event_team_stand_wanted(uuid, timestamptz);
DROP FUNCTION IF EXISTS event_team_formed(uuid, timestamptz);
DROP INDEX IF EXISTS event_teams_unformed_idx;
ALTER TABLE event_teams
    DROP CONSTRAINT IF EXISTS event_teams_formed_by_check,
    DROP COLUMN IF EXISTS formed_by,
    DROP COLUMN IF EXISTS formed_at;
