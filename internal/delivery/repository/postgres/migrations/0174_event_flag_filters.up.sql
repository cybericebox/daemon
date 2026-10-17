-- The flags already issued per task variant of an event exercise (bloom filter
-- or exact bitmap), so a new team gets a flag no other team has. One row per
-- (event exercise, variant, subtask), written in place under the event
-- exercise's variant pick lock.
CREATE TABLE event_flag_filters (
    event_exercise_id uuid     NOT NULL REFERENCES event_exercises (id) ON DELETE CASCADE,
    variant_index     integer  NOT NULL,
    task_id           uuid     NOT NULL,
    fingerprint       bytea    NOT NULL,                        -- SHA-256 of the candidate list the filter was built for
    mode              smallint NOT NULL CHECK (mode IN (1, 2)), -- 1 bloom, 2 bitmap
    salt              bytea    NOT NULL,
    hashes            smallint NOT NULL,
    size              bigint   NOT NULL,                        -- bits: m (bloom) or N (bitmap)
    bits              bytea    NOT NULL,
    inserted          integer  NOT NULL,
    capacity          integer  NOT NULL,
    resets            integer  NOT NULL DEFAULT 0,
    PRIMARY KEY (event_exercise_id, variant_index, task_id)
);
