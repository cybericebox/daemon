-- The CPU throttling request has no page of its own to open (the resources page is the reservation calendar), so
-- it carries no link and no hint to look there. The platform templates already in the database still hold the old
-- link and wording; a link an admin changed is left alone.
UPDATE notification_in_app_templates
SET link = '',
    body = replace(replace(body, ' Перегляньте ресурси.', ''), ' Check the resources.', ''),
    updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'resources.cpu.throttled'
  AND link = '{{.admin_url}}resources/';
