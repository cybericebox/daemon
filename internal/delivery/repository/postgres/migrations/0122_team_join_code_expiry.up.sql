-- The join link of a team may expire; NULL keeps it valid until the captain
-- regenerates it.
ALTER TABLE event_teams
    ADD COLUMN join_code_expires_at timestamptz;
