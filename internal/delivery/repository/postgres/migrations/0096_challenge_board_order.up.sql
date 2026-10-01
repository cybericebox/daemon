-- Board order is defined inside a group: board_order is the position of a
-- challenge within its group across every set of the event (NULL = after the
-- ordered ones, by set attach time and set order). The participant board and
-- the manage page read the same order.
ALTER TABLE event_challenges
    ADD COLUMN board_order integer;

UPDATE event_challenges ec
SET board_order = ranked.position
FROM (SELECT challenge.id,
             (row_number() OVER (PARTITION BY link.event_id, challenge.group_id
                                 ORDER BY link.created_at, challenge.order_index, challenge.id) - 1)::integer AS position
      FROM event_challenges challenge
      JOIN event_exercises link ON link.id = challenge.event_exercise_id) ranked
WHERE ranked.id = ec.id;

-- Tasks the organizer removed from the event: a source switch (update, fork,
-- revert) does not bring them back.
ALTER TABLE event_exercises
    ADD COLUMN excluded_task_ids uuid[] NOT NULL DEFAULT '{}';

-- Static event scoring with one value for every task that follows the event.
-- NULL keeps the legacy rule: each task's own points.
ALTER TABLE events
    ADD COLUMN static_points integer,
    ADD CONSTRAINT events_static_points_check CHECK (static_points IS NULL OR static_points > 0);

-- The scoring formula learns the event static value (same row type).
CREATE OR REPLACE FUNCTION event_solved_scores_at(p_event_id uuid, p_cutoff timestamptz, p_include_team uuid)
    RETURNS SETOF event_solved_score
    LANGUAGE sql
    STABLE
AS
$$
WITH charge AS (
    SELECT COALESCE((SELECT config.hint_charge_mode FROM event_configs config WHERE config.event_id = p_event_id), 0) AS mode
), solves AS (
    SELECT tc.event_id, tc.event_team_id, tc.id AS team_challenge_id,
           tc.event_challenge_id, solved.solved_at, solved.awarded_points,
           team.hidden,
           CASE WHEN e.static_points IS NOT NULL AND e.scoring_mode = 0
                     AND (e.force_event_scoring OR ec.scoring_mode IS NULL)
                THEN e.static_points ELSE ec.points END AS static_points,
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
           ) AS visible_rank,
           CASE WHEN (SELECT mode FROM charge) = 0 THEN
               (SELECT COALESCE(sum(unlock.cost), 0)
                FROM team_challenge_hint_unlocks unlock
                WHERE unlock.team_challenge_id = tc.id
                  AND unlock.unlocked_at <= solved.solved_at)
           ELSE 0 END AS hint_cost
    FROM team_challenge_solves solved
    JOIN team_challenges tc ON tc.id = solved.team_challenge_id
    JOIN event_teams team ON team.id = tc.event_team_id
    JOIN event_challenges ec ON ec.id = tc.event_challenge_id
    JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.status <> 2
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
       greatest(0, (CASE WHEN hidden THEN static_points
            WHEN scoring_mode = 1 THEN round(max_points - (max_points - min_points) *
                 power(popularity_progress, 2) * (3 - 2 * popularity_progress))::integer
            WHEN scoring_mode = 2 THEN
                CASE WHEN ladder_floor_units <= 1 THEN min_points
                     ELSE round(max_points - (max_points - min_points) *
                          power(ladder_progress, 2) * (3 - 2 * ladder_progress))::integer END
            ELSE COALESCE(awarded_points, static_points) END) - hint_cost)::integer AS points,
       true AS solve
FROM ranked
UNION ALL
SELECT unlock.event_id, unlock.event_team_id, unlock.team_challenge_id, unlock.event_challenge_id,
       unlock.unlocked_at, (-unlock.cost)::integer, false
FROM team_challenge_hint_unlocks unlock
JOIN event_challenges ec ON ec.id = unlock.event_challenge_id
JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.status <> 2
WHERE unlock.event_id = p_event_id
  AND (SELECT mode FROM charge) = 1
  AND unlock.cost > 0
  AND (p_cutoff IS NULL OR unlock.unlocked_at < p_cutoff OR unlock.event_team_id = p_include_team)
$$;
