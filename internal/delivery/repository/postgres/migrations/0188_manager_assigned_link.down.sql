UPDATE notification_in_app_templates
SET link = '', updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'event.manager.assigned'
  AND link = '{{.event_url}}manage/';
