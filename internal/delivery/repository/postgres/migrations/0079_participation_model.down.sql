DROP FUNCTION IF EXISTS event_team_admitted(uuid, boolean, boolean, boolean, integer);
DROP FUNCTION IF EXISTS event_min_team_size(uuid);
DROP FUNCTION IF EXISTS event_team_public_name(boolean, uuid, uuid, text);
DROP FUNCTION IF EXISTS event_participant_public_name(uuid, uuid);
DROP TABLE IF EXISTS event_list_columns;
ALTER TABLE event_teams
    DROP COLUMN IF EXISTS admission_locked,
    DROP COLUMN IF EXISTS admitted_manually,
    DROP COLUMN IF EXISTS individual;
DROP INDEX IF EXISTS event_participants_pseudonym_uq;
ALTER TABLE event_participants
    DROP CONSTRAINT IF EXISTS event_participants_pseudonym_check,
    DROP COLUMN IF EXISTS invitation_sent_at,
    DROP COLUMN IF EXISTS pseudonym;
ALTER TABLE event_configs
    DROP COLUMN IF EXISTS allow_pseudonyms;
