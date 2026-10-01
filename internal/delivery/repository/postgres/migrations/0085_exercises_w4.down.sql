-- Restores the 0082 scoring function and view, then drops the W4 schema.
DO
$$
    BEGIN
        EXECUTE 'DROP VIEW event_solved_scores';
    END
$$;
DROP FUNCTION event_solved_scores_at(uuid, timestamptz, uuid);
DROP TYPE event_solved_score;

CREATE TYPE event_solved_score AS
(
    event_id           uuid,
    event_team_id      uuid,
    team_challenge_id  uuid,
    event_challenge_id uuid,
    solved_at          timestamptz,
    points             integer
);

CREATE FUNCTION event_solved_scores_at(p_event_id uuid, p_cutoff timestamptz, p_include_team uuid)
    RETURNS SETOF event_solved_score
    LANGUAGE sql
    STABLE
AS
$$
WITH solves AS (
    SELECT tc.event_id, tc.event_team_id, tc.id AS team_challenge_id,
           tc.event_challenge_id, solved.solved_at, solved.awarded_points,
           team.hidden, ec.points AS static_points,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.scoring_mode ELSE ec.scoring_mode END AS scoring_mode,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_min_points ELSE ec.dynamic_min_points END AS min_points,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_max_points ELSE ec.dynamic_max_points END AS max_points,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_floor_at_percent ELSE ec.dynamic_floor_at_percent END AS floor_percent,
           greatest(1, least(COALESCE(population.units_count, 1),
               (SELECT count(*) FROM event_teams population_team
                WHERE population_team.event_id = e.id AND NOT population_team.hidden))) AS population,
           count(*) FILTER (WHERE NOT team.hidden) OVER (PARTITION BY tc.event_challenge_id) AS visible_solve_count,
           count(*) FILTER (WHERE NOT team.hidden) OVER (
               PARTITION BY tc.event_challenge_id
               ORDER BY solved.solved_at, tc.id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
           ) AS visible_rank
    FROM team_challenge_solves solved
    JOIN team_challenges tc ON tc.id = solved.team_challenge_id
    JOIN event_teams team ON team.id = tc.event_team_id
    JOIN event_challenges ec ON ec.id = tc.event_challenge_id
    JOIN events e ON e.id = tc.event_id
    LEFT JOIN event_scoring_populations population ON population.event_id = e.id
    WHERE tc.event_id = p_event_id
      AND (p_cutoff IS NULL OR solved.solved_at < p_cutoff OR tc.event_team_id = p_include_team)
), progress AS (
    SELECT solves.*,
           least(1::numeric, visible_solve_count::numeric /
                 greatest(1::numeric, population::numeric * floor_percent::numeric / 100)) AS popularity_progress,
           ceil(population::numeric * floor_percent::numeric / 100) AS ladder_floor_units
    FROM solves
), ranked AS (
    SELECT progress.*,
           least(1::numeric, greatest(0::numeric,
                 (visible_rank - 1)::numeric / greatest(1::numeric, ladder_floor_units - 1))) AS ladder_progress
    FROM progress
)
SELECT event_id, event_team_id, team_challenge_id, event_challenge_id, solved_at,
       CASE WHEN hidden THEN static_points
            WHEN scoring_mode = 1 THEN round(max_points - (max_points - min_points) *
                 power(popularity_progress, 2) * (3 - 2 * popularity_progress))::integer
            WHEN scoring_mode = 2 THEN
                CASE WHEN ladder_floor_units <= 1 THEN min_points
                     ELSE round(max_points - (max_points - min_points) *
                          power(ladder_progress, 2) * (3 - 2 * ladder_progress))::integer END
            ELSE COALESCE(awarded_points, static_points) END::integer AS points
FROM ranked
$$;

-- The view keeps its columns and now delegates to the function. It is created
-- through EXECUTE because sqlc cannot resolve set-returning function columns in
-- a view; its catalog keeps the identical 0074 column shape.
DO
$$
    BEGIN
        EXECUTE 'CREATE VIEW event_solved_scores AS
            SELECT score.event_id, score.event_team_id, score.team_challenge_id,
                   score.event_challenge_id, score.solved_at, score.points
            FROM events e
            CROSS JOIN LATERAL event_solved_scores_at(e.id, NULL, NULL) score';
    END
$$;

ALTER TABLE event_configs
    DROP COLUMN hint_charge_mode;

DROP TABLE team_challenge_hint_unlocks;

ALTER TABLE team_challenges
    DROP COLUMN hints;

ALTER TABLE event_challenges
    DROP COLUMN hints,
    DROP COLUMN hint_costs;

UPDATE event_exercises SET status = 1 WHERE status = 2;
ALTER TABLE event_exercises
    DROP COLUMN detached_at,
    DROP COLUMN detached_by,
    DROP CONSTRAINT event_exercises_status_check,
    ADD CONSTRAINT event_exercises_status_check CHECK (status IN (0, 1));

DROP FUNCTION exercise_has_infrastructure(uuid);
DROP FUNCTION exercise_readable_by(uuid, uuid);
DROP FUNCTION exercise_available_to_event(uuid, uuid);
DROP TABLE exercise_proposals;
DROP TABLE exercise_event_access;

DROP INDEX exercises_forked_from_idx;
DROP INDEX exercises_owner_event_idx;
DROP INDEX exercises_event_name_uq;
DROP INDEX exercises_catalog_name_uq;
ALTER TABLE exercises
    DROP CONSTRAINT exercises_scope_owner_check,
    DROP COLUMN forked_from_version_id,
    DROP COLUMN forked_from_exercise_id,
    DROP COLUMN origin_event_id,
    DROP COLUMN access_level,
    DROP COLUMN owner_event_id,
    DROP COLUMN scope,
    ADD CONSTRAINT exercises_name_key UNIQUE (name);
