-- Owner wording (2026-09-29): the failure notice is «Лабораторія впала»,
-- never «стенд». Only the seeded platform texts change; a template an admin
-- already edited keeps its wording.
UPDATE notification_in_app_templates
SET title = 'Лабораторія впала',
    body  = 'Лабораторія команди «{{.team_name}}» не працює. Перевірте її на сторінці «Лабораторії».'
WHERE notification_type = 'event.lab.failed'
  AND scope_event_id IS NULL
  AND status = 'published'
  AND title = 'Помилка стенда';

UPDATE notification_email_templates t
SET subject   = 'Лабораторія впала: команда «{{.team_name}}»',
    preheader = 'Лабораторія команди «{{.team_name}}» не працює',
    body      = jsonb_set(t.body, ARRAY[(SELECT (block_index - 1)::text
                                         FROM jsonb_array_elements(t.body) WITH ORDINALITY AS block(value, block_index)
                                         WHERE value->>'type' = 'rich_text' LIMIT 1)],
                          '{"type": "rich_text", "content": {"root": {"type": "root", "children": [{"type": "paragraph", "children": [{"type": "text", "text": "Лабораторія команди «", "format": 0}, {"type": "variable", "varName": "team_name"}, {"type": "text", "text": "» не працює. Перевірте її на сторінці «Лабораторії».", "format": 0}]}, {"type": "paragraph", "children": [{"type": "text", "text": "Захід «", "format": 0}, {"type": "variable", "varName": "event_name"}, {"type": "text", "text": "». Причина: ", "format": 0}, {"type": "variable", "varName": "reason"}, {"type": "text", "text": ".", "format": 0}]}]}}}'::jsonb)
WHERE t.notification_type = 'event.lab.failed'
  AND t.scope_event_id IS NULL
  AND t.status = 'published'
  AND t.subject = 'Помилка стенда команди «{{.team_name}}»'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(t.body) AS block(value) WHERE value->>'type' = 'rich_text');
