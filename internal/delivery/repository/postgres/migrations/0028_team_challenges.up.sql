CREATE TABLE team_challenges
(
    id                 uuid        PRIMARY KEY,
    event_id           uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    event_team_id      uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    event_challenge_id uuid        NOT NULL REFERENCES event_challenges (id) ON DELETE RESTRICT,
    variant_index      integer     NOT NULL CHECK (variant_index >= 0),
    snapshot           jsonb       NOT NULL,
    expected_flag      text        NOT NULL,
    readiness          smallint    NOT NULL CHECK (readiness IN (0, 1, 2, 3)),
    solved_at          timestamptz,
    created_at         timestamptz NOT NULL,
    UNIQUE (event_team_id, event_challenge_id)
);

CREATE INDEX team_challenges_event_team_idx ON team_challenges (event_id, event_team_id);
CREATE INDEX team_challenges_challenge_idx ON team_challenges (event_challenge_id);
