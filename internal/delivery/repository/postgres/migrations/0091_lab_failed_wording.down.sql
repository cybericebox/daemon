UPDATE notification_in_app_templates
SET title = 'Помилка стенда',
    body  = 'Стенд команди «{{.team_name}}» на заході «{{.event_name}}» не розгорнуто: {{.reason}}.'
WHERE notification_type = 'event.lab.failed'
  AND scope_event_id IS NULL
  AND status = 'published'
  AND title = 'Лабораторія впала';

UPDATE notification_email_templates t
SET subject   = 'Помилка стенда команди «{{.team_name}}»',
    preheader = 'Стенд потребує вашої уваги',
    body      = jsonb_set(t.body, ARRAY[(SELECT (block_index - 1)::text
                                         FROM jsonb_array_elements(t.body) WITH ORDINALITY AS block(value, block_index)
                                         WHERE value->>'type' = 'rich_text' LIMIT 1)],
                          '{"type": "rich_text", "content": {"root": {"type": "root", "children": [{"type": "paragraph", "children": [{"type": "text", "text": "Стенд команди «", "format": 0}, {"type": "variable", "varName": "team_name"}, {"type": "text", "text": "» на заході «", "format": 0}, {"type": "variable", "varName": "event_name"}, {"type": "text", "text": "» не вдалося розгорнути. Причина: ", "format": 0}, {"type": "variable", "varName": "reason"}, {"type": "text", "text": ".", "format": 0}]}, {"type": "paragraph", "children": [{"type": "text", "text": "Завдання зі стендом не відкриються, доки стенди не будуть готові в усіх команд. Перевірте розділ «Стенди» і перестворіть стенд.", "format": 0}]}]}}}'::jsonb)
WHERE t.notification_type = 'event.lab.failed'
  AND t.scope_event_id IS NULL
  AND t.status = 'published'
  AND t.subject = 'Лабораторія впала: команда «{{.team_name}}»'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(t.body) AS block(value) WHERE value->>'type' = 'rich_text');
