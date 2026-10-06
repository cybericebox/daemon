-- Inbox follow-ups (INBOX-DESIGN §8).
--
-- 1. Two system resolutions: an Event finish expires its open applications,
--    a deleted applicant withdraws theirs.
ALTER TABLE in_app_notifications
    DROP CONSTRAINT in_app_notifications_resolution_check,
    ADD CONSTRAINT in_app_notifications_resolution_check
        CHECK (resolution IN ('approved', 'rejected', 'fixed', 'resolved', 'expired', 'withdrawn'));

-- 2. The latest decision per request subject. A copy delivered after the
--    decision (the dispatch is asynchronous) is created already resolved when
--    the decision is not older than the request itself.
CREATE TABLE inbox_subject_resolutions
(
    subject_ref text PRIMARY KEY,
    resolution  text        NOT NULL
        CHECK (resolution IN ('approved', 'rejected', 'fixed', 'resolved', 'expired', 'withdrawn')),
    resolved_by uuid,
    resolved_at timestamptz NOT NULL
);

-- 3. Legacy lab failures (before 0090) become open requests: a recipient
--    closes each by hand (they have no subject, so a re-created lab cannot).
UPDATE in_app_notifications
SET action_required = true
WHERE notification_type = 'event.lab.failed'
  AND category = 'requests'
  AND resolved_at IS NULL
  AND NOT action_required;

-- 4. participant.event.results_published: a moderator opened the results
--    («Відкрити підсумки»). In-app on for participants (managers get an
--    in-app copy from the planner), email off by default.
INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
VALUES ('participant.event.results_published', 'in_app', true, '{"kind":"all_participants"}'::jsonb),
       ('participant.event.results_published', 'email', false, '{"kind":"all_participants"}'::jsonb)
ON CONFLICT (signal_type, channel) DO NOTHING;

INSERT INTO notification_in_app_templates
    (id, notification_type, status, title, body, link, icon, tone, surface,
     auto_dismiss_ms, actions, dismissible, published_at)
SELECT gen_random_uuid(), 'participant.event.results_published', 'published', 'Підсумки відкрито',
       'Результати заходу «{{.event_name}}» вже доступні.', '{{event_url}}', 'trophy', 'info',
       surface, auto_dismiss_ms, actions, dismissible, now()
FROM notification_in_app_templates
WHERE notification_type = 'participant.event.finished'
  AND scope_event_id IS NULL
  AND status = 'published';

INSERT INTO notification_email_templates
    (id, notification_type, status, subject, preheader, body, styling, published_at)
SELECT gen_random_uuid(), 'participant.event.results_published', 'published',
       'Підсумки заходу «{{.event_name}}» відкрито', 'Результати вже доступні',
       jsonb_set(body, ARRAY[(SELECT (block_index - 1)::text
                              FROM jsonb_array_elements(body) WITH ORDINALITY AS block(value, block_index)
                              WHERE value->>'type' = 'rich_text' LIMIT 1)],
                 '{"type": "rich_text", "content": {"root": {"type": "root", "children": [{"type": "paragraph", "children": [{"type": "text", "text": "Підсумки заходу «", "format": 0}, {"type": "variable", "varName": "event_name"}, {"type": "text", "text": "» відкрито.", "format": 0}]}, {"type": "paragraph", "children": [{"type": "text", "text": "Результати вже доступні на сайті заходу.", "format": 0}]}]}}}'::jsonb),
       styling, now()
FROM notification_email_templates
WHERE notification_type = 'participant.event.finished'
  AND scope_event_id IS NULL
  AND status = 'published';
