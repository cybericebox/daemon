ALTER TABLE event_participants
    ADD COLUMN invited_team_id uuid REFERENCES event_teams(id) ON DELETE SET NULL,
    ADD COLUMN invited_to_team boolean NOT NULL DEFAULT false;

INSERT INTO notification_email_templates
    (id, notification_type, status, subject, preheader, body, styling, published_at)
SELECT gen_random_uuid(), 'participant.team_invitation.sent', 'published',
       'Запрошення до команди «{{.team_name}}»', 'Вас запросили до команди на заході',
       jsonb_set(body, ARRAY[(SELECT (block_index - 1)::text
                              FROM jsonb_array_elements(body) WITH ORDINALITY AS block(value, block_index)
                              WHERE value->>'type' = 'rich_text' LIMIT 1)],
           '{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Вас запросили до команди «","format":0},{"type":"variable","varName":"team_name"},{"type":"text","text":"» на заході «","format":0},{"type":"variable","varName":"event_name"},{"type":"text","text":"». Прийміть запрошення за кнопкою нижче.","format":0}]}]}}}'::jsonb),
       styling, now()
FROM notification_email_templates
WHERE notification_type = 'participant.invitation.sent'
  AND scope_event_id IS NULL
  AND status = 'published';

INSERT INTO notification_in_app_templates
    (id, notification_type, status, title, body, link, icon, tone, surface,
     auto_dismiss_ms, actions, dismissible, published_at)
SELECT gen_random_uuid(), 'participant.team_invitation.sent', 'published',
       'Запрошення до команди', 'Вас запросили до команди «{{.team_name}}» на заході «{{.event_name}}».',
       '{{invite_url}}', icon, tone, surface, auto_dismiss_ms, actions, dismissible, now()
FROM notification_in_app_templates
WHERE notification_type = 'participant.invitation.sent'
  AND scope_event_id IS NULL
  AND status = 'published';

INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
VALUES ('participant.team_invitation.sent', 'email', false, '{"kind":"signal_subject"}'::jsonb),
       ('participant.team_invitation.sent', 'in_app', false, '{"kind":"signal_subject"}'::jsonb)
ON CONFLICT (signal_type, channel) DO NOTHING;
