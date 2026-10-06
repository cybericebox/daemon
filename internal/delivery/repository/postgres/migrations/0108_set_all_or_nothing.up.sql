-- A set and its tasks are all-or-nothing (they share one infrastructure).
--
-- 1. Tasks removed one by one come back: every task of the pinned
--    version's canonical variant (the fixed one when pinned, else the
--    first) that has no board challenge gets one, with default points and
--    the event overrides empty. The stand engine then prepares the team
--    assignments of the new challenges.
INSERT INTO event_challenges (id, event_exercise_id, task_id, order_index, points, hints_enabled, published, snapshot, created_at, hints, hint_costs)
SELECT gen_random_uuid(), link.id, (task->>'id')::uuid,
       COALESCE((SELECT max(ec.order_index) FROM event_challenges ec WHERE ec.event_exercise_id = link.id), -1)
           + row_number() OVER (PARTITION BY link.id ORDER BY task_order)::integer,
       100, false, false,
       jsonb_strip_nulls(jsonb_build_object(
           'name', btrim(task->>'name'),
           'description', task->'description',
           'difficulty', task->'difficulty',
           'attachments', CASE WHEN jsonb_array_length(COALESCE(task->'attachments', '[]'::jsonb)) > 0 THEN task->'attachments' END)),
       now(),
       COALESCE((SELECT jsonb_agg(jsonb_build_object('id', hint->'id', 'level', COALESCE(hint->>'level', 'nudge'), 'text', hint->'text') ORDER BY hint_order)
                 FROM jsonb_array_elements(COALESCE(task->'hints', '[]'::jsonb)) WITH ORDINALITY AS hints(hint, hint_order)), '[]'::jsonb),
       '{}'::jsonb
FROM event_exercises link
JOIN exercise_versions version ON version.id = link.exercise_version_id
CROSS JOIN LATERAL jsonb_array_elements(
    version.variants -> (CASE WHEN link.variant_mode = 1 AND link.fixed_variant_index IS NOT NULL
                                  AND link.fixed_variant_index < jsonb_array_length(version.variants)
                              THEN link.fixed_variant_index ELSE 0 END) -> 'tasks'
) WITH ORDINALITY AS tasks(task, task_order)
WHERE link.status = 0
  AND cardinality(link.excluded_task_ids) > 0
  AND NOT EXISTS (SELECT 1 FROM event_challenges ec WHERE ec.event_exercise_id = link.id AND ec.task_id = (task->>'id')::uuid);

ALTER TABLE event_exercises
    DROP COLUMN excluded_task_ids;

-- 2. Visibility is per set: a set is shown when any of its tasks was shown.
UPDATE event_challenges ec
SET published = shown.any_published
FROM (SELECT event_exercise_id, bool_or(published) AS any_published
      FROM event_challenges
      GROUP BY event_exercise_id) shown
WHERE shown.event_exercise_id = ec.event_exercise_id
  AND ec.published <> shown.any_published;
