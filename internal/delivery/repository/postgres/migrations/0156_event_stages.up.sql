-- An event may have an ordered list of stages. Order is by opens_at (no position column: stages never overlap, so
-- time is the order). All stages lie inside the event window; that rule spans two tables and is checked by the use
-- case (events.start_at / finish_at are editable), the table only guarantees a sane, non-overlapping interval.
CREATE TABLE event_stages
(
    id         uuid        PRIMARY KEY,
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    opens_at   timestamptz NOT NULL,
    closes_at  timestamptz NOT NULL,
    -- "Можна повернутися": after closes_at the stage's tasks stay open until the event finishes. Editable until the
    -- stage closes (use case rule); the table cannot see "now" so it does not lock it.
    returnable boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT event_stages_name_check CHECK (btrim(name) <> '' AND char_length(name) <= 100),
    CONSTRAINT event_stages_window_check CHECK (opens_at < closes_at),
    -- Gaps are breaks; adjacent stages (closes_at = next opens_at) are allowed because the range is half-open.
    CONSTRAINT event_stages_no_overlap EXCLUDE USING gist (event_id WITH =, tstzrange(opens_at, closes_at) WITH &&),
    -- Target of the composite FK below: a set can only point at a stage of its own event.
    CONSTRAINT event_stages_id_event_key UNIQUE (id, event_id)
);

CREATE UNIQUE INDEX event_stages_event_name_idx ON event_stages (event_id, lower(btrim(name)));
CREATE INDEX event_stages_event_opens_idx ON event_stages (event_id, opens_at);

-- A stage is attached to an exercise set (the event exercise whose visibility the manage page toggles). NULL = the
-- set lives for the whole event, exactly as before. NO ACTION (not RESTRICT): deleting an event cascades to both
-- tables in one statement and must not trip over this reference; deleting a stage that still has sets fails.
ALTER TABLE event_exercises
    ADD COLUMN stage_id uuid,
    ADD CONSTRAINT event_exercises_stage_fkey
        FOREIGN KEY (stage_id, event_id) REFERENCES event_stages (id, event_id);

CREATE INDEX event_exercises_stage_idx ON event_exercises (stage_id) WHERE stage_id IS NOT NULL;

-- The deploy lead is now computed by the platform (spec: Labs); no production DB yet, so the organizer setting goes.
ALTER TABLE event_configs
    DROP COLUMN stand_deploy_lead_minutes;

-- Practice solves (spec: Scoring). After a RETURNABLE stage closes the rating of that stage is frozen; a correct
-- submission is still verified and shown to the participant, but it is stored here, NOT in team_challenge_solves,
-- so results, scoring, prerequisites, first blood and analytics never see it.
CREATE TABLE team_challenge_practice_solves
(
    team_challenge_id uuid        PRIMARY KEY REFERENCES team_challenges (id) ON DELETE CASCADE,
    solved_at         timestamptz NOT NULL
);

-- Practice attempts (after the stage closed) are marked so attempt-limit counting, results and analytics skip them.
ALTER TABLE challenge_attempts
    ADD COLUMN practice boolean NOT NULL DEFAULT false;

-- Finish-countdown mode, one setting for the event, applied to the end of every stage (0119 added the sibling
-- show_*/finish_countdown_minutes columns to event_configs; CountdownSettings is persisted there, column per field).
--   0 before_end (default, today's behaviour: visible only the last finish_countdown_minutes)
--   1 from_start (visible from the beginning of the current stage; of the event when it has no stages)
ALTER TABLE event_configs
    ADD COLUMN finish_countdown_mode smallint NOT NULL DEFAULT 0,
    ADD CONSTRAINT event_configs_finish_countdown_mode_check CHECK (finish_countdown_mode IN (0, 1));

-- The one definition of a stage phase, used by the board, submissions, hints and lab access queries.
--   NULL stage (opens_at IS NULL) -> 1 open (whole-event set: today's behaviour)
--   0 upcoming        now < opens_at          hidden, no access
--   1 open            opens_at <= now < closes_at
--   2 ended, returnable                       still open until the event finishes (the event lifecycle ends it)
--   3 closed          not returnable          visible on the board, no submissions / hints / lab access
CREATE FUNCTION event_stage_phase(opens_at timestamptz, closes_at timestamptz, returnable boolean, at timestamptz)
    RETURNS smallint
    LANGUAGE sql IMMUTABLE
AS
$$
SELECT CASE WHEN opens_at IS NULL THEN 1
            WHEN at < opens_at THEN 0
            WHEN at < closes_at THEN 1
            WHEN returnable THEN 2
            ELSE 3 END::smallint
$$;

-- Number of stage boundaries of an event that have passed: every opens_at, and every closes_at of a
-- non-returnable stage (a returnable stage's close changes no access). The lab access sync stores the epoch it
-- applied; a different epoch makes the sync dirty, so the existing 2-second sync job wakes at each boundary.
-- 0 for an event without stages.
CREATE FUNCTION event_stage_epoch(event uuid, at timestamptz)
    RETURNS integer
    LANGUAGE sql STABLE
AS
$$
SELECT (count(*) FILTER (WHERE s.opens_at <= at)
        + count(*) FILTER (WHERE NOT s.returnable AND s.closes_at <= at))::integer
FROM event_stages s
WHERE s.event_id = event
$$;

ALTER TABLE event_lab_access_syncs
    ADD COLUMN applied_stage_epoch integer NOT NULL DEFAULT 0;

-- Rating reads must never see practice attempts: effective_challenge_attempts (used by results, scoring, attempt
-- limits, solve integrity and analytics) now skips them in one place. The participant attempt history reads the
-- _all view, which also carries the practice flag. Same columns, so dependent functions and views stay valid.
CREATE VIEW effective_challenge_attempts_all AS
SELECT ca.id,
       ca.event_id,
       ca.event_team_id,
       ca.team_challenge_id,
       ca.user_id,
       ca.answer,
       ca.correct AS automatic_correct,
       ca.received_at,
       ca.created_at,
       COALESCE(latest.id, '00000000-0000-0000-0000-000000000000'::uuid) AS decision_id,
       COALESCE(latest.decision, 0) AS decision,
       COALESCE(latest.reason, '')::text AS decision_reason,
       COALESCE(latest.decided_by, '00000000-0000-0000-0000-000000000000'::uuid) AS decided_by,
       COALESCE(latest.decided_at, 'epoch'::timestamptz) AS decided_at,
       CASE COALESCE(latest.decision, 0)
           WHEN 1 THEN TRUE
           WHEN 2 THEN FALSE
           ELSE ca.correct
       END::boolean AS effective_correct,
       ca.practice
FROM challenge_attempts ca
LEFT JOIN LATERAL (
    SELECT cad.id, cad.decision, cad.reason, cad.decided_by, cad.decided_at
    FROM challenge_attempt_decisions cad
    WHERE cad.challenge_attempt_id = ca.id
    ORDER BY cad.decided_at DESC, cad.id DESC
    LIMIT 1
) latest ON TRUE;

CREATE OR REPLACE VIEW effective_challenge_attempts AS
SELECT id, event_id, event_team_id, team_challenge_id, user_id, answer, automatic_correct, received_at, created_at, decision_id, decision, decision_reason, decided_by, decided_at, effective_correct
FROM effective_challenge_attempts_all
WHERE NOT practice;
