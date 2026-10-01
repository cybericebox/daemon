ALTER TABLE event_configs
    ADD COLUMN max_team_size integer NOT NULL DEFAULT 5,
    ADD COLUMN min_team_size integer,
    ADD COLUMN max_teams integer,
    ADD CONSTRAINT event_configs_team_size_check CHECK (
        max_team_size > 0
        AND (min_team_size IS NULL OR (min_team_size > 0 AND min_team_size <= max_team_size))
        AND (max_teams IS NULL OR max_teams > 0)
    );

CREATE TABLE event_teams
(
    id           uuid         PRIMARY KEY,
    event_id     uuid         NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    name         varchar(64)  NOT NULL,
    join_code    varchar(128) NOT NULL,
    captain_id   uuid         NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    hidden       boolean      NOT NULL DEFAULT false,
    member_count integer      NOT NULL DEFAULT 1 CHECK (member_count > 0),
    created_at   timestamptz  NOT NULL,
    updated_at   timestamptz  NOT NULL,
    UNIQUE (event_id, name),
    UNIQUE (event_id, join_code)
);

CREATE INDEX event_teams_event_created_idx ON event_teams (event_id, created_at DESC, id DESC);

ALTER TABLE event_participants
    ADD COLUMN team_id uuid REFERENCES event_teams (id) ON DELETE SET NULL,
    ADD COLUMN team_role smallint,
    ADD CONSTRAINT event_participants_team_role_check CHECK (team_role IS NULL OR team_role IN (0, 1));

CREATE INDEX event_participants_team_idx ON event_participants (team_id, user_id)
    WHERE team_id IS NOT NULL;
