CREATE TABLE event_exercises
(
    id                  uuid        PRIMARY KEY,
    event_id            uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    exercise_id         uuid        NOT NULL REFERENCES exercises (id) ON DELETE RESTRICT,
    exercise_version_id uuid        NOT NULL REFERENCES exercise_versions (id) ON DELETE RESTRICT,
    variant_mode        smallint    NOT NULL,
    fixed_variant_index integer,
    created_at          timestamptz NOT NULL,
    created_by          uuid        REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT event_exercises_variant_mode_check CHECK (
        (variant_mode = 0 AND fixed_variant_index IS NULL)
        OR (variant_mode = 1 AND fixed_variant_index IS NOT NULL AND fixed_variant_index >= 0)
    ),
    UNIQUE (event_id, exercise_id)
);

CREATE INDEX event_exercises_event_created_idx ON event_exercises (event_id, created_at DESC, id DESC);
