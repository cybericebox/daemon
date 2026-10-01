CREATE VIEW event_solved_scores AS
SELECT tc.event_id, tc.event_team_id, tc.id AS team_challenge_id, tc.event_challenge_id, solved.solved_at,
       CASE WHEN (CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.scoring_mode ELSE ec.scoring_mode END) = 1
            THEN round((CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_max_points ELSE ec.dynamic_max_points END)
                 - ((CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_max_points ELSE ec.dynamic_max_points END)
                    - (CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_min_points ELSE ec.dynamic_min_points END))
                   * power(least(1::numeric, count(*) OVER (PARTITION BY tc.event_challenge_id)::numeric / greatest(1::numeric, COALESCE(population.units_count, 1)::numeric * (CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_floor_at_percent ELSE ec.dynamic_floor_at_percent END)::numeric / 100)), 2)
                   * (3 - 2 * least(1::numeric, count(*) OVER (PARTITION BY tc.event_challenge_id)::numeric / greatest(1::numeric, COALESCE(population.units_count, 1)::numeric * (CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_floor_at_percent ELSE ec.dynamic_floor_at_percent END)::numeric / 100))))::integer
            ELSE COALESCE(solved.awarded_points, ec.points) END AS points
FROM team_challenge_solves solved
JOIN team_challenges tc ON tc.id = solved.team_challenge_id
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
JOIN events e ON e.id = tc.event_id
LEFT JOIN event_scoring_populations population ON population.event_id = e.id;
