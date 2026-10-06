ALTER TABLE events
    ADD COLUMN scoring_mode smallint NOT NULL DEFAULT 0,
    ADD COLUMN dynamic_algorithm smallint NOT NULL DEFAULT 0,
    ADD COLUMN dynamic_min_points integer NOT NULL DEFAULT 1,
    ADD COLUMN dynamic_max_points integer NOT NULL DEFAULT 100,
    ADD COLUMN dynamic_floor_at_percent integer NOT NULL DEFAULT 100,
    ADD COLUMN force_event_scoring boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT events_scoring_mode_check CHECK (scoring_mode IN (0, 1, 2, 3)),
    ADD CONSTRAINT events_dynamic_algorithm_check CHECK (dynamic_algorithm IN (0, 1, 2)),
    ADD CONSTRAINT events_dynamic_bounds_check CHECK (
        dynamic_min_points > 0 AND dynamic_max_points > dynamic_min_points
        AND dynamic_floor_at_percent BETWEEN 1 AND 100
    );

ALTER TABLE event_challenges
    ADD COLUMN scoring_mode smallint,
    ADD COLUMN dynamic_algorithm smallint,
    ADD COLUMN dynamic_min_points integer,
    ADD COLUMN dynamic_max_points integer,
    ADD COLUMN dynamic_floor_at_percent integer,
    ADD CONSTRAINT event_challenges_scoring_profile_check CHECK (
        (scoring_mode IS NULL AND dynamic_algorithm IS NULL AND dynamic_min_points IS NULL
            AND dynamic_max_points IS NULL AND dynamic_floor_at_percent IS NULL)
        OR (scoring_mode = 0 AND dynamic_algorithm IS NULL AND dynamic_min_points IS NULL
            AND dynamic_max_points IS NULL AND dynamic_floor_at_percent IS NULL)
        OR (scoring_mode IN (1, 2, 3) AND dynamic_algorithm IS NOT NULL
            AND dynamic_min_points IS NOT NULL AND dynamic_max_points IS NOT NULL
            AND dynamic_floor_at_percent IS NOT NULL
            AND dynamic_algorithm IN (0, 1, 2)
            AND dynamic_min_points > 0 AND dynamic_max_points > dynamic_min_points
            AND dynamic_floor_at_percent BETWEEN 1 AND 100)
    );

CREATE TABLE event_scoring_populations (
    event_id uuid PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    units_count integer NOT NULL CHECK (units_count > 0),
    captured_at timestamptz NOT NULL
);
