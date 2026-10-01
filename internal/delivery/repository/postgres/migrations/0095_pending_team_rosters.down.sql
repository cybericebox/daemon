-- Empty pending rosters cannot satisfy the old rule; drop them first.
DELETE FROM event_teams WHERE member_count = 0;
ALTER TABLE event_teams
    DROP CONSTRAINT IF EXISTS event_teams_member_count_check,
    ADD CONSTRAINT event_teams_member_count_check CHECK (member_count > 0);
