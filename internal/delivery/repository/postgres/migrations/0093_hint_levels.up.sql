-- Hint levels: a catalog hint no longer has a cost, only a level (JSON field
-- in the exercise version, defaulted to "nudge" on read). The price is set per
-- event board challenge (hint_costs); a hint without an override is free.
--
-- Boards already materialized kept the catalog cost inside their hints JSON.
-- Move a non-zero catalog cost into an override so running events keep their
-- prices; an existing override wins.
UPDATE event_challenges ec
SET hint_costs = (SELECT COALESCE(jsonb_object_agg(h ->> 'id', (h ->> 'cost')::int), '{}'::jsonb)
                  FROM jsonb_array_elements(ec.hints) h
                  WHERE jsonb_typeof(h -> 'cost') = 'number'
                    AND (h ->> 'cost')::int > 0) || ec.hint_costs
WHERE EXISTS (SELECT 1
              FROM jsonb_array_elements(ec.hints) h
              WHERE jsonb_typeof(h -> 'cost') = 'number'
                AND (h ->> 'cost')::int > 0);
