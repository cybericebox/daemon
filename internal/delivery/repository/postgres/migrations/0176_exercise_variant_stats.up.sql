-- Variant statistics: counters per task version and variant that only grow. A variant is issued when a team of a
-- real event gets it, solved when that team solved every subtask of it there. The moderators team (tests and
-- previews) never counts. Deleting an event, rejecting a solve or switching the event to another version never
-- decreases a counter.
CREATE TABLE exercise_variant_stats
(
    exercise_version_id uuid   NOT NULL REFERENCES exercise_versions (id) ON DELETE CASCADE,
    variant_id          uuid   NOT NULL,
    issued              bigint NOT NULL DEFAULT 0,
    solved              bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (exercise_version_id, variant_id)
);

CREATE TABLE exercise_variant_task_stats
(
    exercise_version_id uuid   NOT NULL REFERENCES exercise_versions (id) ON DELETE CASCADE,
    variant_id          uuid   NOT NULL,
    task_id             uuid   NOT NULL,
    solved              bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (exercise_version_id, variant_id, task_id)
);

-- Dedup marks: one row per counted fact. No foreign keys: they outlive deleted events, so the same fact can never
-- bump a counter twice (a rebuilt solve projection, a re-set variant, a re-created row).
CREATE TABLE exercise_variant_issue_marks
(
    event_team_id       uuid NOT NULL,
    event_exercise_id   uuid NOT NULL,
    exercise_version_id uuid NOT NULL,
    variant_id          uuid NOT NULL,
    PRIMARY KEY (event_team_id, event_exercise_id, exercise_version_id, variant_id)
);

CREATE TABLE exercise_variant_solve_marks
(
    event_team_id       uuid NOT NULL,
    event_exercise_id   uuid NOT NULL,
    exercise_version_id uuid NOT NULL,
    variant_id          uuid NOT NULL,
    task_id             uuid NOT NULL,
    PRIMARY KEY (event_team_id, event_exercise_id, exercise_version_id, variant_id, task_id)
);

CREATE TABLE exercise_variant_full_marks
(
    event_team_id       uuid NOT NULL,
    event_exercise_id   uuid NOT NULL,
    exercise_version_id uuid NOT NULL,
    variant_id          uuid NOT NULL,
    PRIMARY KEY (event_team_id, event_exercise_id, exercise_version_id, variant_id)
);

-- The id of the variant at an index of a version's variant set; NULL when there is no such variant or its id is
-- not a uuid (legacy content never fails the write that counts it).
CREATE FUNCTION exercise_variant_stats_variant_id(p_variants jsonb, p_index integer) RETURNS uuid
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT CASE
           WHEN raw ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN raw::uuid
           END
FROM (SELECT p_variants -> p_index ->> 'id' AS raw) v
$$;

-- The number of subtasks of the variant at an index; 0 when unknown.
CREATE FUNCTION exercise_variant_stats_task_count(p_variants jsonb, p_index integer) RETURNS integer
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT CASE WHEN jsonb_typeof(p_variants -> p_index -> 'tasks') = 'array'
                THEN jsonb_array_length(p_variants -> p_index -> 'tasks')
            ELSE 0 END
$$;

-- Issue: a team gets a variant of an event exercise (create, re-pick, or the rewrite of a version switch).
-- Primary-key lookups only.
CREATE FUNCTION exercise_variant_stats_issue() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
DECLARE
    v_event_exercise uuid;
    v_version        uuid;
    v_variant        uuid;
    v_rows           integer;
BEGIN
    IF EXISTS (SELECT 1 FROM event_teams WHERE id = NEW.event_team_id AND moderators) THEN
        RETURN NULL;
    END IF;
    SELECT ee.id, ee.exercise_version_id, exercise_variant_stats_variant_id(v.variants, NEW.variant_index)
    INTO v_event_exercise, v_version, v_variant
    FROM event_challenges ec
             JOIN event_exercises ee ON ee.id = ec.event_exercise_id
             JOIN exercise_versions v ON v.id = ee.exercise_version_id
    WHERE ec.id = NEW.event_challenge_id;
    IF v_variant IS NULL THEN
        RETURN NULL;
    END IF;

    INSERT INTO exercise_variant_issue_marks (event_team_id, event_exercise_id, exercise_version_id, variant_id)
    VALUES (NEW.event_team_id, v_event_exercise, v_version, v_variant)
    ON CONFLICT DO NOTHING;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    IF v_rows > 0 THEN
        INSERT INTO exercise_variant_stats (exercise_version_id, variant_id, issued)
        VALUES (v_version, v_variant, 1)
        ON CONFLICT (exercise_version_id, variant_id) DO UPDATE SET issued = exercise_variant_stats.issued + 1;
    END IF;
    RETURN NULL;
END
$$;

-- Solve: a subtask solve counts once per team, event exercise, version and variant; the variant counts as solved
-- once every subtask of it is solved. Primary-key lookups only.
CREATE FUNCTION exercise_variant_stats_solve() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
DECLARE
    v_team           uuid;
    v_event_exercise uuid;
    v_version        uuid;
    v_variant        uuid;
    v_task           uuid;
    v_tasks          integer;
    v_marks          integer;
    v_rows           integer;
BEGIN
    SELECT tc.event_team_id,
           ee.id,
           ee.exercise_version_id,
           exercise_variant_stats_variant_id(v.variants, tc.variant_index),
           ec.task_id,
           exercise_variant_stats_task_count(v.variants, tc.variant_index)
    INTO v_team, v_event_exercise, v_version, v_variant, v_task, v_tasks
    FROM team_challenges tc
             JOIN event_teams team ON team.id = tc.event_team_id AND NOT team.moderators
             JOIN event_challenges ec ON ec.id = tc.event_challenge_id
             JOIN event_exercises ee ON ee.id = ec.event_exercise_id
             JOIN exercise_versions v ON v.id = ee.exercise_version_id
    WHERE tc.id = NEW.team_challenge_id;
    IF v_variant IS NULL THEN
        RETURN NULL;
    END IF;

    INSERT INTO exercise_variant_solve_marks (event_team_id, event_exercise_id, exercise_version_id, variant_id, task_id)
    VALUES (v_team, v_event_exercise, v_version, v_variant, v_task)
    ON CONFLICT DO NOTHING;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    IF v_rows = 0 THEN
        RETURN NULL;
    END IF;

    -- Serializes the last-subtask check of one team's variant: two subtasks solved at once still see each
    -- other's mark (each statement reads after the other transaction committed).
    PERFORM pg_advisory_xact_lock(hashtextextended(
            'exercise_variant_stats:' || v_team || ':' || v_event_exercise || ':' || v_version || ':' || v_variant, 0));

    INSERT INTO exercise_variant_task_stats (exercise_version_id, variant_id, task_id, solved)
    VALUES (v_version, v_variant, v_task, 1)
    ON CONFLICT (exercise_version_id, variant_id, task_id) DO UPDATE SET solved = exercise_variant_task_stats.solved + 1;

    SELECT count(*)
    INTO v_marks
    FROM exercise_variant_solve_marks
    WHERE event_team_id = v_team
      AND event_exercise_id = v_event_exercise
      AND exercise_version_id = v_version
      AND variant_id = v_variant;
    IF v_tasks = 0 OR v_marks < v_tasks THEN
        RETURN NULL;
    END IF;

    INSERT INTO exercise_variant_full_marks (event_team_id, event_exercise_id, exercise_version_id, variant_id)
    VALUES (v_team, v_event_exercise, v_version, v_variant)
    ON CONFLICT DO NOTHING;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    IF v_rows > 0 THEN
        INSERT INTO exercise_variant_stats (exercise_version_id, variant_id, solved)
        VALUES (v_version, v_variant, 1)
        ON CONFLICT (exercise_version_id, variant_id) DO UPDATE SET solved = exercise_variant_stats.solved + 1;
    END IF;
    RETURN NULL;
END
$$;

CREATE TRIGGER team_challenges_variant_stats
    AFTER INSERT OR UPDATE OF variant_index
    ON team_challenges
    FOR EACH ROW
EXECUTE FUNCTION exercise_variant_stats_issue();

CREATE TRIGGER team_challenge_solves_variant_stats
    AFTER INSERT
    ON team_challenge_solves
    FOR EACH ROW
EXECUTE FUNCTION exercise_variant_stats_solve();

-- Backfill: marks from every existing assignment and solve by the same rules, then the counters from the marks.
-- Idempotent: marks are inserted once and counters never go below their marks.
CREATE FUNCTION exercise_variant_stats_backfill() RETURNS void
    LANGUAGE plpgsql
AS
$$
BEGIN
    INSERT INTO exercise_variant_issue_marks (event_team_id, event_exercise_id, exercise_version_id, variant_id)
    SELECT DISTINCT tc.event_team_id, ee.id, ee.exercise_version_id, x.variant_id
    FROM team_challenges tc
             JOIN event_teams team ON team.id = tc.event_team_id AND NOT team.moderators
             JOIN event_challenges ec ON ec.id = tc.event_challenge_id
             JOIN event_exercises ee ON ee.id = ec.event_exercise_id
             JOIN exercise_versions v ON v.id = ee.exercise_version_id
             CROSS JOIN LATERAL (SELECT exercise_variant_stats_variant_id(v.variants, tc.variant_index) AS variant_id) x
    WHERE x.variant_id IS NOT NULL
    ON CONFLICT DO NOTHING;

    INSERT INTO exercise_variant_solve_marks (event_team_id, event_exercise_id, exercise_version_id, variant_id, task_id)
    SELECT DISTINCT tc.event_team_id, ee.id, ee.exercise_version_id, x.variant_id, ec.task_id
    FROM team_challenge_solves solved
             JOIN team_challenges tc ON tc.id = solved.team_challenge_id
             JOIN event_teams team ON team.id = tc.event_team_id AND NOT team.moderators
             JOIN event_challenges ec ON ec.id = tc.event_challenge_id
             JOIN event_exercises ee ON ee.id = ec.event_exercise_id
             JOIN exercise_versions v ON v.id = ee.exercise_version_id
             CROSS JOIN LATERAL (SELECT exercise_variant_stats_variant_id(v.variants, tc.variant_index) AS variant_id) x
    WHERE x.variant_id IS NOT NULL
    ON CONFLICT DO NOTHING;

    INSERT INTO exercise_variant_full_marks (event_team_id, event_exercise_id, exercise_version_id, variant_id)
    SELECT m.event_team_id, m.event_exercise_id, m.exercise_version_id, m.variant_id
    FROM (SELECT event_team_id, event_exercise_id, exercise_version_id, variant_id, count(*) AS solved
          FROM exercise_variant_solve_marks
          GROUP BY event_team_id, event_exercise_id, exercise_version_id, variant_id) m
             JOIN exercise_versions v ON v.id = m.exercise_version_id
             CROSS JOIN LATERAL (SELECT jsonb_array_length(variant -> 'tasks') AS tasks
                                 FROM jsonb_array_elements(v.variants) variant
                                 WHERE jsonb_typeof(v.variants) = 'array'
                                   AND jsonb_typeof(variant -> 'tasks') = 'array'
                                   AND variant ->> 'id' = m.variant_id::text
                                 LIMIT 1) t
    WHERE t.tasks > 0
      AND m.solved >= t.tasks
    ON CONFLICT DO NOTHING;

    INSERT INTO exercise_variant_stats (exercise_version_id, variant_id, issued, solved)
    SELECT c.exercise_version_id, c.variant_id, sum(c.issued)::bigint, sum(c.solved)::bigint
    FROM (SELECT exercise_version_id, variant_id, count(*) AS issued, 0 AS solved
          FROM exercise_variant_issue_marks
          GROUP BY exercise_version_id, variant_id
          UNION ALL
          SELECT exercise_version_id, variant_id, 0, count(*)
          FROM exercise_variant_full_marks
          GROUP BY exercise_version_id, variant_id) c
    WHERE EXISTS (SELECT 1 FROM exercise_versions v WHERE v.id = c.exercise_version_id)
    GROUP BY c.exercise_version_id, c.variant_id
    ON CONFLICT (exercise_version_id, variant_id) DO UPDATE
        SET issued = GREATEST(exercise_variant_stats.issued, EXCLUDED.issued),
            solved = GREATEST(exercise_variant_stats.solved, EXCLUDED.solved);

    INSERT INTO exercise_variant_task_stats (exercise_version_id, variant_id, task_id, solved)
    SELECT m.exercise_version_id, m.variant_id, m.task_id, count(*)
    FROM exercise_variant_solve_marks m
    WHERE EXISTS (SELECT 1 FROM exercise_versions v WHERE v.id = m.exercise_version_id)
    GROUP BY m.exercise_version_id, m.variant_id, m.task_id
    ON CONFLICT (exercise_version_id, variant_id, task_id) DO UPDATE
        SET solved = GREATEST(exercise_variant_task_stats.solved, EXCLUDED.solved);
END
$$;

SELECT exercise_variant_stats_backfill();
