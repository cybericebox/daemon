ALTER TABLE event_participants
    ADD COLUMN invited_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN invited boolean NOT NULL DEFAULT false;

UPDATE notification_email_templates
SET body = jsonb_insert(body, '{0}', '{"type":"button","label":"Прийняти запрошення","url":"{{invite_url}}","align":"left"}'::jsonb, true)
WHERE notification_type = 'participant.invitation.sent'
  AND status = 'published'
  AND jsonb_typeof(body) = 'array'
  AND NOT body @> '[{"type":"button","url":"{{invite_url}}"}]'::jsonb;
