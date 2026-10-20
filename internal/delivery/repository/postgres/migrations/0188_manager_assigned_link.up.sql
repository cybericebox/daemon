-- «Вам надано доступ до заходу» opens the event's staff pages. The platform templates already in the
-- database carry an empty link; ones an admin edited (non-empty link) are left alone.
UPDATE notification_in_app_templates
SET link = '{{.event_url}}manage/', updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'event.manager.assigned'
  AND link = '';
