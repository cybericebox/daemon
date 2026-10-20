-- The wording is not restored: the old hint pointed at a page that shows nothing about the request.
UPDATE notification_in_app_templates
SET link = '{{.admin_url}}resources/', updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'resources.cpu.throttled'
  AND link = '';
