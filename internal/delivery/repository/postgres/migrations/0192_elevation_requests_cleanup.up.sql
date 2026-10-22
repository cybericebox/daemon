-- Elevation requests (exercise.elevation.requested) that nothing could close: the task was deleted (its
-- elevation rows cascade) or the request was decided while an inbox copy stayed open. They are not manually
-- resolvable, so they hung in «Запити» forever. Each open copy is closed for every recipient, exactly as the
-- domain resolution would (in_app_notifications + the remembered decision in inbox_subject_resolutions):
--   * the elevation row is gone                  -> 'withdrawn', by nobody;
--   * the elevation was approved (1) / rejected (2) -> 'approved' / 'rejected', by the admin who decided.
-- A copy whose elevation is still pending (0) stays open. Idempotent: a second run finds nothing open.
WITH stale AS (
    SELECT DISTINCT ON (n.subject_ref)
        n.subject_ref,
        CASE e.status WHEN 1 THEN 'approved' WHEN 2 THEN 'rejected' ELSE 'withdrawn' END AS resolution,
        e.decided_by AS resolved_by
    FROM in_app_notifications n
             LEFT JOIN exercise_resource_elevations e ON 'elevation:' || e.id::text = n.subject_ref
    WHERE n.notification_type = 'exercise.elevation.requested'
      AND n.action_required
      AND n.resolved_at IS NULL
      AND n.subject_ref LIKE 'elevation:%'
      AND (e.id IS NULL OR e.status <> 0)
    ORDER BY n.subject_ref
), decision AS (
    INSERT INTO inbox_subject_resolutions (subject_ref, resolution, resolved_by, resolved_at)
    SELECT subject_ref, resolution, resolved_by, now()
    FROM stale
    ON CONFLICT (subject_ref) DO UPDATE
        SET resolution = EXCLUDED.resolution, resolved_by = EXCLUDED.resolved_by, resolved_at = EXCLUDED.resolved_at
)
UPDATE in_app_notifications n
SET resolved_at = now(),
    resolution  = stale.resolution,
    resolved_by = stale.resolved_by,
    read_at     = COALESCE(n.read_at, now())
FROM stale
WHERE n.subject_ref = stale.subject_ref
  AND n.action_required
  AND n.resolved_at IS NULL;

-- The request now opens its page in the admin site. The platform templates already in the database carry an empty
-- link; one an admin changed (non-empty link) is left alone.
UPDATE notification_in_app_templates
SET link = '{{.admin_url}}elevations/detail/?id={{.elevation_id}}', updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'exercise.elevation.requested'
  AND link = '';
