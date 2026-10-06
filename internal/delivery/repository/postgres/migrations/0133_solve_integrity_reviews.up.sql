-- «Доброчесність»: the stored parts. The signals themselves are computed on
-- read; only an organizer's decisions are kept.

-- A «перевірено» note on one flagged team task (a solve, or a task flagged
-- before any solve). It goes away with the task or the event.
CREATE TABLE solve_integrity_reviews
(
    team_challenge_id uuid PRIMARY KEY REFERENCES team_challenges (id) ON DELETE CASCADE,
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    note              text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 1000),
    reviewed_by       uuid        REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at       timestamptz NOT NULL
);

CREATE INDEX solve_integrity_reviews_event_idx ON solve_integrity_reviews (event_id, reviewed_at);

-- «Не підсвічувати такі випадки»: a signal kind on a catalog task, with the key
-- that makes the case (for shared_wrong the normalized wrong value, for the
-- other kinds empty). scope 'event' reaches one event; scope 'exercise' every
-- event that uses the catalog exercise. task_id is event_challenges.task_id,
-- stable across the exercise's versions.
CREATE TABLE integrity_dismissals
(
    id          uuid PRIMARY KEY,
    scope       text        NOT NULL CHECK (scope IN ('event', 'exercise')),
    event_id    uuid REFERENCES events (id) ON DELETE CASCADE,
    exercise_id uuid        NOT NULL REFERENCES exercises (id) ON DELETE CASCADE,
    task_id     uuid        NOT NULL,
    kind        text        NOT NULL CHECK (kind IN ('shared_wrong', 'no_access', 'no_lab', 'too_fast',
                                                     'first_try_hard', 'brute_force', 'follows_solve')),
    key         text        NOT NULL DEFAULT '' CHECK (char_length(key) <= 200),
    note        text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 1000),
    created_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL,
    CHECK ((scope = 'event') = (event_id IS NOT NULL))
);

CREATE UNIQUE INDEX integrity_dismissals_event_uq
    ON integrity_dismissals (event_id, exercise_id, task_id, kind, key) WHERE scope = 'event';
CREATE UNIQUE INDEX integrity_dismissals_exercise_uq
    ON integrity_dismissals (exercise_id, task_id, kind, key) WHERE scope = 'exercise';
