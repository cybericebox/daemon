ALTER TABLE event_stages
    ADD COLUMN opens_with_event boolean NOT NULL DEFAULT false,
    ADD COLUMN closes_with_event boolean NOT NULL DEFAULT false;

-- Preserve the positional bindings of the previous application exactly once.
-- New stages default to custom sources; runtime never infers a source from position or equality.
WITH legacy AS (
    SELECT s.id,
           row_number() OVER (PARTITION BY s.event_id ORDER BY s.opens_at, s.id) AS first_position,
           row_number() OVER (PARTITION BY s.event_id ORDER BY s.opens_at DESC, s.id DESC) AS last_position
    FROM event_stages s
)
UPDATE event_stages s
SET opens_with_event = legacy.first_position = 1 AND s.opens_at = e.start_at,
    closes_with_event = legacy.last_position = 1 AND e.finish_at IS NOT NULL AND s.closes_at = e.finish_at
FROM legacy, events e
WHERE s.id = legacy.id AND s.event_id = e.id AND e.lifecycle_configured;
