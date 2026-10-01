CREATE OR REPLACE VIEW event_solved_scores AS
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
            ELSE COALESCE(awarded_points, static_points) END AS points
FROM ranked;
