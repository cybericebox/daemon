-- A moderator may build a team from invitees who have not accepted yet: the
-- team exists with its captain and members pending, so no one counts as a
-- member until they accept (member_count = 0 is a valid, not admitted team).
ALTER TABLE event_teams
    DROP CONSTRAINT IF EXISTS event_teams_member_count_check,
    ADD CONSTRAINT event_teams_member_count_check CHECK (member_count >= 0);
