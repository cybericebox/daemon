-- Dynamic scoring may decay to 0: min ≥ 0, max > min (event and task).
ALTER TABLE events
    DROP CONSTRAINT events_dynamic_bounds_check,
    ADD CONSTRAINT events_dynamic_bounds_check CHECK (
        dynamic_min_points >= 0 AND dynamic_max_points > dynamic_min_points
        AND dynamic_floor_at_percent BETWEEN 1 AND 100
    );

ALTER TABLE event_challenges
    DROP CONSTRAINT event_challenges_scoring_profile_check,
    ADD CONSTRAINT event_challenges_scoring_profile_check CHECK (
        (scoring_mode IS NULL AND dynamic_algorithm IS NULL AND dynamic_min_points IS NULL
            AND dynamic_max_points IS NULL AND dynamic_floor_at_percent IS NULL)
        OR (scoring_mode = 0 AND dynamic_algorithm IS NULL AND dynamic_min_points IS NULL
            AND dynamic_max_points IS NULL AND dynamic_floor_at_percent IS NULL)
        OR (scoring_mode IN (1, 2, 3) AND dynamic_algorithm IS NOT NULL
            AND dynamic_min_points IS NOT NULL AND dynamic_max_points IS NOT NULL
            AND dynamic_floor_at_percent IS NOT NULL
            AND dynamic_algorithm IN (0, 1, 2)
            AND dynamic_min_points >= 0 AND dynamic_max_points > dynamic_min_points
            AND dynamic_floor_at_percent BETWEEN 1 AND 100)
    );
