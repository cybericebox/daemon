CREATE TABLE challenge_attempts
(
    id                uuid        PRIMARY KEY,
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    event_team_id     uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    team_challenge_id uuid        NOT NULL REFERENCES team_challenges (id) ON DELETE RESTRICT,
    user_id           uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    answer            text        NOT NULL,
    correct           boolean     NOT NULL,
    received_at       timestamptz NOT NULL,
    created_at        timestamptz NOT NULL
);

CREATE INDEX challenge_attempts_team_challenge_received_idx
    ON challenge_attempts (team_challenge_id, received_at ASC);
CREATE INDEX challenge_attempts_event_team_received_idx
    ON challenge_attempts (event_id, event_team_id, received_at ASC);
