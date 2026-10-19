UPDATE notification_in_app_templates
SET link = '', updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type IN ('resources.change.requested', 'resources.alarm.raised')
  AND link = '{{.admin_url}}resources/';
