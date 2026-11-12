-- CPU throttling of a task device is no request for an admin to decide: the alerts go to the error journal now.
-- The open ones raised before (hundreds on one event day, one per device per lab run) are closed as resolved and
-- marked read, like the system does when their lab run is gone. Nothing is deleted. The decision of each subject is
-- remembered, so a copy still in delivery is created resolved.
WITH resolved AS (
    UPDATE in_app_notifications
    SET resolved_at = now(),
        resolution  = 'resolved',
        read_at     = COALESCE(read_at, now())
    WHERE subject_ref LIKE 'cpu_throttle:%'
      AND action_required
      AND resolved_at IS NULL
    RETURNING subject_ref
)
INSERT INTO inbox_subject_resolutions (subject_ref, resolution, resolved_by, resolved_at)
SELECT DISTINCT subject_ref, 'resolved', NULL::uuid, now() FROM resolved
ON CONFLICT (subject_ref) DO NOTHING;

-- Whatever was resolved earlier but never read.
UPDATE in_app_notifications SET read_at = now() WHERE subject_ref LIKE 'cpu_throttle:%' AND read_at IS NULL;
