-- Variant statistics: grow-only facts per task version and variant. A variant is issued when a team of a real event
-- gets it; a subtask of it is solved when that team solves it there. The counts (issued, per-subtask solves, teams
-- that solved every subtask) are aggregated when read. The moderators team (tests and previews) never counts.
-- Marks are never deleted and have no foreign keys: deleting an event, rejecting a solve or switching the event to
-- another version never decreases a count, and the same fact never counts twice (a rebuilt solve projection, a
-- re-set variant). event_team_id is event-scoped, so a mark is one team at one event.
CREATE TABLE exercise_variant_issue_marks
(
    event_team_id       uuid NOT NULL,
    exercise_version_id uuid NOT NULL,
    variant_id          uuid NOT NULL,
    PRIMARY KEY (event_team_id, exercise_version_id, variant_id)
);

CREATE INDEX exercise_variant_issue_marks_variant_idx ON exercise_variant_issue_marks (exercise_version_id, variant_id);

CREATE TABLE exercise_variant_solve_marks
(
    event_team_id       uuid NOT NULL,
    exercise_version_id uuid NOT NULL,
    variant_id          uuid NOT NULL,
    task_id             uuid NOT NULL,
    PRIMARY KEY (event_team_id, exercise_version_id, variant_id, task_id)
);

CREATE INDEX exercise_variant_solve_marks_variant_idx ON exercise_variant_solve_marks (exercise_version_id, variant_id);

-- The id of the variant at an index of a version's variant set; NULL when there is no such variant (a negative
-- index would address from the end) or its id is not a uuid.
CREATE FUNCTION exercise_variant_stats_variant_id(p_variants jsonb, p_index integer) RETURNS uuid
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT CASE
           WHEN p_index >= 0 AND raw ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               THEN raw::uuid
           END
FROM (SELECT CASE WHEN jsonb_typeof(p_variants) = 'array' THEN p_variants -> p_index ->> 'id' END AS raw) v
$$;

-- The triggers only insert marks (primary-key lookups, ON CONFLICT DO NOTHING): no counter row, no lock. A failure
-- is only a warning, so statistics never fail or serialize an assignment or a flag submission.
CREATE FUNCTION exercise_variant_stats_issue() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
BEGIN
    BEGIN
        INSERT INTO exercise_variant_issue_marks (event_team_id, exercise_version_id, variant_id)
        SELECT NEW.event_team_id, ee.exercise_version_id, x.variant_id
        FROM event_teams team
                 JOIN event_challenges ec ON ec.id = NEW.event_challenge_id
                 JOIN event_exercises ee ON ee.id = ec.event_exercise_id
                 JOIN exercise_versions v ON v.id = ee.exercise_version_id
                 CROSS JOIN LATERAL (SELECT exercise_variant_stats_variant_id(v.variants, NEW.variant_index) AS variant_id) x
        WHERE team.id = NEW.event_team_id
          AND NOT team.moderators
          AND x.variant_id IS NOT NULL
        ON CONFLICT DO NOTHING;
    EXCEPTION
        WHEN OTHERS THEN RAISE WARNING 'exercise_variant_stats: issue mark skipped: %', SQLERRM;
    END;
    RETURN NULL;
END
$$;

CREATE FUNCTION exercise_variant_stats_solve() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
BEGIN
    BEGIN
        INSERT INTO exercise_variant_solve_marks (event_team_id, exercise_version_id, variant_id, task_id)
        SELECT tc.event_team_id, ee.exercise_version_id, x.variant_id, ec.task_id
        FROM team_challenges tc
                 JOIN event_teams team ON team.id = tc.event_team_id AND NOT team.moderators
                 JOIN event_challenges ec ON ec.id = tc.event_challenge_id
                 JOIN event_exercises ee ON ee.id = ec.event_exercise_id
                 JOIN exercise_versions v ON v.id = ee.exercise_version_id
                 CROSS JOIN LATERAL (SELECT exercise_variant_stats_variant_id(v.variants, tc.variant_index) AS variant_id) x
        WHERE tc.id = NEW.team_challenge_id
          AND x.variant_id IS NOT NULL
        ON CONFLICT DO NOTHING;
    EXCEPTION
        WHEN OTHERS THEN RAISE WARNING 'exercise_variant_stats: solve mark skipped: %', SQLERRM;
    END;
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

-- Backfill: marks from every existing assignment and solve by the same rules. Idempotent (marks insert once).
-- A variant id is resolved once per distinct (version, variant index).
CREATE FUNCTION exercise_variant_stats_backfill() RETURNS void
    LANGUAGE plpgsql
AS
$$
BEGIN
    WITH assigned AS (SELECT DISTINCT tc.event_team_id, ee.exercise_version_id, tc.variant_index
                      FROM team_challenges tc
                               JOIN event_teams team ON team.id = tc.event_team_id AND NOT team.moderators
                               JOIN event_challenges ec ON ec.id = tc.event_challenge_id
                               JOIN event_exercises ee ON ee.id = ec.event_exercise_id),
         resolved AS (SELECT k.exercise_version_id, k.variant_index,
                             exercise_variant_stats_variant_id(v.variants, k.variant_index) AS variant_id
                      FROM (SELECT DISTINCT exercise_version_id, variant_index FROM assigned) k
                               JOIN exercise_versions v ON v.id = k.exercise_version_id)
    INSERT
    INTO exercise_variant_issue_marks (event_team_id, exercise_version_id, variant_id)
    SELECT a.event_team_id, a.exercise_version_id, r.variant_id
    FROM assigned a
             JOIN resolved r USING (exercise_version_id, variant_index)
    WHERE r.variant_id IS NOT NULL
    ON CONFLICT DO NOTHING;

    WITH solved AS (SELECT DISTINCT tc.event_team_id, ee.exercise_version_id, tc.variant_index, ec.task_id
                    FROM team_challenge_solves s
                             JOIN team_challenges tc ON tc.id = s.team_challenge_id
                             JOIN event_teams team ON team.id = tc.event_team_id AND NOT team.moderators
                             JOIN event_challenges ec ON ec.id = tc.event_challenge_id
                             JOIN event_exercises ee ON ee.id = ec.event_exercise_id),
         resolved AS (SELECT k.exercise_version_id, k.variant_index,
                             exercise_variant_stats_variant_id(v.variants, k.variant_index) AS variant_id
                      FROM (SELECT DISTINCT exercise_version_id, variant_index FROM solved) k
                               JOIN exercise_versions v ON v.id = k.exercise_version_id)
    INSERT
    INTO exercise_variant_solve_marks (event_team_id, exercise_version_id, variant_id, task_id)
    SELECT s.event_team_id, s.exercise_version_id, r.variant_id, s.task_id
    FROM solved s
             JOIN resolved r USING (exercise_version_id, variant_index)
    WHERE r.variant_id IS NOT NULL
    ON CONFLICT DO NOTHING;
END
$$;

SELECT exercise_variant_stats_backfill();
