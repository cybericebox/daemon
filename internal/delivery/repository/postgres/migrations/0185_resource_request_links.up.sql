-- The resource requests link to the admin resources page. Published platform templates seeded at startup
-- (see startupdefaults.Seed) are not rewritten when the defaults change, so the empty link is filled here.
UPDATE notification_in_app_templates
SET link = '{{.admin_url}}resources/', updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type IN ('resources.change.requested', 'resources.alarm.raised')
  AND link = '';
