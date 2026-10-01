CREATE TABLE moderator_flag_checks
(
    id                uuid        PRIMARY KEY,
    team_challenge_id uuid        NOT NULL REFERENCES team_challenges (id) ON DELETE CASCADE,
    checked_at        timestamptz NOT NULL
);

CREATE INDEX moderator_flag_checks_team_challenge_checked_idx
    ON moderator_flag_checks (team_challenge_id, checked_at);

DROP INDEX team_challenge_solves_order_idx;
DROP FUNCTION event_team_visible(boolean, boolean, uuid, boolean, boolean, boolean, integer);
ALTER TABLE event_teams DROP CONSTRAINT event_teams_moderators_hidden;
