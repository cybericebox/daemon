-- W10: flag submission rate limit. Participant attempts are counted on
-- challenge_attempts (existing indexes cover both windows). The moderators
-- board check records no attempt, so its throttle keeps a short-lived log.
CREATE TABLE moderator_flag_checks
(
    id                uuid        PRIMARY KEY,
    team_challenge_id uuid        NOT NULL REFERENCES team_challenges (id) ON DELETE CASCADE,
    checked_at        timestamptz NOT NULL
);

CREATE INDEX moderator_flag_checks_team_challenge_checked_idx
    ON moderator_flag_checks (team_challenge_id, checked_at);
