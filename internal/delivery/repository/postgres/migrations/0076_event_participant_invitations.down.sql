ALTER TABLE event_participants DROP COLUMN invited_by, DROP COLUMN invited;

UPDATE notification_email_templates
SET body = body #- '{1}'
WHERE notification_type = 'participant.invitation.sent'
  AND status = 'published'
  AND body #>> '{1,url}' = '{{invite_url}}';
