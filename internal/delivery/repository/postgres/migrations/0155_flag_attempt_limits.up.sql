-- The maximum number of wrong flag submissions a team may make per task. The event value applies to every task of
-- the event; an event challenge (an attached task) may override it. NULL on the event means unlimited, NULL on the
-- challenge means "use the event value". Attempts are not stored in a counter: they are counted from
-- challenge_attempts (existing index on team_challenge_id), so changing a limit applies to future submissions at once
-- and past attempts stay counted.
ALTER TABLE event_configs
    ADD COLUMN max_flag_attempts integer,
    ADD CONSTRAINT event_configs_max_flag_attempts_check CHECK (max_flag_attempts IS NULL OR max_flag_attempts BETWEEN 1 AND 1000);

ALTER TABLE event_challenges
    ADD COLUMN max_flag_attempts integer,
    ADD CONSTRAINT event_challenges_max_flag_attempts_check CHECK (max_flag_attempts IS NULL OR max_flag_attempts BETWEEN 1 AND 1000);
