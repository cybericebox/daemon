-- The closed requests are not reopened: their elevation is gone or decided, so nothing could close them again.
UPDATE notification_in_app_templates
SET link = '', updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'exercise.elevation.requested'
  AND link = '{{.admin_url}}elevations/detail/?id={{.elevation_id}}';
