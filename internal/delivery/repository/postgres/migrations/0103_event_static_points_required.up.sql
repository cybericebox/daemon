-- Static event scoring always has its one value (tasks «Як у заходу» and
-- forced scoring use it). Existing events without it get the most common
-- points of their tasks that follow the event (ties: the higher value), or
-- 100 without such tasks. So scores do not change, a following task whose
-- points differ keeps them as its own static scoring (scoring_mode 0).
-- Forced static events cannot keep per-task points: they take the most
-- common value for every task (the owner accepted this for the rare case).
WITH chosen AS (
    SELECT e.id,
           COALESCE((SELECT ec.points
                     FROM event_challenges ec
                     JOIN event_exercises ee ON ee.id = ec.event_exercise_id
                     WHERE ee.event_id = e.id
                       AND (e.force_event_scoring OR ec.scoring_mode IS NULL)
                     GROUP BY ec.points
                     ORDER BY count(*) DESC, ec.points DESC
                     LIMIT 1), 100) AS points
    FROM events e
    WHERE e.static_points IS NULL
), own AS (
    UPDATE event_challenges ec
    SET scoring_mode = 0
    FROM event_exercises ee, events e, chosen
    WHERE ee.id = ec.event_exercise_id
      AND e.id = ee.event_id
      AND chosen.id = e.id
      AND e.scoring_mode = 0
      AND NOT e.force_event_scoring
      AND ec.scoring_mode IS NULL
      AND ec.points <> chosen.points
)
UPDATE events e
SET static_points = chosen.points
FROM chosen
WHERE chosen.id = e.id;

ALTER TABLE events
    ALTER COLUMN static_points SET DEFAULT 100,
    ADD CONSTRAINT events_static_points_required_check CHECK (scoring_mode <> 0 OR static_points IS NOT NULL);
