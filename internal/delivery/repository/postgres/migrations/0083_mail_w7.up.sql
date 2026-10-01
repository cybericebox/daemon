-- W7 mail: SMTP settings in the database, Event mail settings, delivery
-- journal columns, Event-scoped in-app inbox and new participant mail.

-- One platform row (scope_event_id IS NULL) and at most one row per Event.
-- The password is sealed by the application (AES-256-GCM, row-bound context).
CREATE TABLE mail_smtp_configs
(
    id                  uuid PRIMARY KEY,
    scope_event_id      uuid UNIQUE REFERENCES events (id) ON DELETE CASCADE,
    host                text        NOT NULL,
    port                integer     NOT NULL CHECK (port BETWEEN 1 AND 65535),
    tls_mode            text        NOT NULL CHECK (tls_mode IN ('starttls', 'tls')),
    username            text        NOT NULL DEFAULT '',
    password_ciphertext text        NOT NULL DEFAULT '',
    from_name           text        NOT NULL DEFAULT '',
    from_address        text        NOT NULL DEFAULT '',
    reply_to            text        NOT NULL DEFAULT '',
    updated_by          uuid,
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX mail_smtp_configs_platform_idx ON mail_smtp_configs ((true)) WHERE scope_event_id IS NULL;

-- Missing row = defaults (no contact address, reminder 24 h before start).
CREATE TABLE event_mail_settings
(
    event_id                uuid PRIMARY KEY REFERENCES events (id) ON DELETE CASCADE,
    contact_email           text        NOT NULL DEFAULT '',
    start_reminder_hours    integer     NOT NULL DEFAULT 24 CHECK (start_reminder_hours BETWEEN 0 AND 168),
    start_reminder_sent_for timestamptz,
    finished_notified_for   timestamptz,
    updated_at              timestamptz NOT NULL DEFAULT now()
);

-- Already finished events never get a late "finished" mail.
INSERT INTO event_mail_settings (event_id, finished_notified_for)
SELECT id, LEAST(manual_finished_at, finish_at)
FROM events
WHERE lifecycle_configured
  AND LEAST(manual_finished_at, finish_at) <= now()
ON CONFLICT (event_id) DO NOTHING;

ALTER TABLE event_participants
    ADD COLUMN invitation_expired_notified_at timestamptz;

-- Invitations of already finished events are expired long ago: no mail.
UPDATE event_participants p
SET invitation_expired_notified_at = now()
FROM events e
WHERE e.id = p.event_id
  AND p.invited AND p.status = 1
  AND LEAST(e.manual_finished_at, e.finish_at) <= now();

-- Delivery journal.
ALTER TABLE notification_dispatches
    ADD COLUMN scope_event_id uuid;
CREATE INDEX notification_dispatches_event_idx
    ON notification_dispatches (scope_event_id, created_at DESC, id DESC)
    WHERE scope_event_id IS NOT NULL;

ALTER TABLE notification_dispatch_targets
    ADD COLUMN transport      text NOT NULL DEFAULT '',
    ADD COLUMN recipient      text NOT NULL DEFAULT '',
    ADD COLUMN fallback_error text NOT NULL DEFAULT '';

-- In-app inbox per Event (M5).
ALTER TABLE in_app_notifications
    ADD COLUMN scope_event_id uuid;
CREATE INDEX in_app_notifications_user_event_idx
    ON in_app_notifications (user_id, scope_event_id, created_at DESC);

-- M4: application submitted / approved / rejected are on by default.
UPDATE platform_signal_notification_defaults
SET enabled = true
WHERE signal_type IN ('participant.approval_registration.submitted',
                      'participant.approval_registration.approved',
                      'participant.approval_registration.rejected');

-- Revoked / expired invitations notify the invitee by default.
UPDATE platform_signal_notification_defaults
SET enabled = true
WHERE signal_type IN ('participant.invitation.revoked', 'participant.invitation.expired');

-- Declined: the invitee acted themselves; no participant message.
DELETE FROM event_signal_notification_subscriptions WHERE signal_type = 'participant.invitation.declined';
DELETE FROM platform_signal_notification_defaults WHERE signal_type = 'participant.invitation.declined';

-- New Event-wide participant messages: start reminder and event finished.
INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
SELECT s.signal_type, c.channel, true, '{"kind":"all_participants"}'::jsonb
FROM (VALUES ('participant.event.start_reminder'), ('participant.event.finished')) AS s(signal_type)
CROSS JOIN (VALUES ('email'), ('in_app')) AS c(channel)
ON CONFLICT (signal_type, channel) DO NOTHING;

INSERT INTO notification_email_templates
    (id, notification_type, status, subject, preheader, body, styling, published_at)
SELECT gen_random_uuid(), seed.notification_type, 'published', seed.subject, seed.preheader,
       jsonb_set(body, ARRAY[(SELECT (block_index - 1)::text
                              FROM jsonb_array_elements(body) WITH ORDINALITY AS block(value, block_index)
                              WHERE value->>'type' = 'rich_text' LIMIT 1)], seed.rich_text)
         || jsonb_build_array(jsonb_build_object('type', 'button', 'label', seed.button_label,
                                                 'url', '{{event_url}}', 'align', 'left')),
       styling, now()
FROM notification_email_templates,
     (VALUES
        ('participant.event.start_reminder', 'Захід «{{.event_name}}» починається {{.start_at}}',
         'Нагадування про старт', 'Перейти до заходу',
         '{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Нагадуємо: захід «","format":0},{"type":"variable","varName":"event_name"},{"type":"text","text":"» починається ","format":0},{"type":"variable","varName":"start_at"},{"type":"text","text":" (за київським часом).","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Перевірте, що можете увійти до облікового запису, і приєднуйтеся вчасно.","format":0}]}]}}}'::jsonb),
        ('participant.event.finished', 'Захід «{{.event_name}}» завершено',
         'Дякуємо за участь', 'Переглянути результати',
         '{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Захід «","format":0},{"type":"variable","varName":"event_name"},{"type":"text","text":"» завершено. Дякуємо за участь!","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Результати доступні на сайті заходу, щойно організатори їх відкриють.","format":0}]}]}}}'::jsonb)
     ) AS seed(notification_type, subject, preheader, button_label, rich_text)
WHERE notification_email_templates.notification_type = 'participant.invitation.revoked'
  AND notification_email_templates.scope_event_id IS NULL
  AND notification_email_templates.status = 'published';

INSERT INTO notification_in_app_templates
    (id, notification_type, status, title, body, link, icon, tone, surface,
     auto_dismiss_ms, actions, dismissible, published_at)
SELECT gen_random_uuid(), seed.notification_type, 'published', seed.title, seed.body,
       '{{event_url}}', seed.icon, 'info', surface, auto_dismiss_ms, actions, dismissible, now()
FROM notification_in_app_templates,
     (VALUES
        ('participant.event.start_reminder', 'Скоро старт',
         'Захід «{{.event_name}}» починається {{.start_at}} (за київським часом).', 'calendar'),
        ('participant.event.finished', 'Захід завершено',
         'Захід «{{.event_name}}» завершено. Дякуємо за участь!', 'trophy')
     ) AS seed(notification_type, title, body, icon)
WHERE notification_in_app_templates.notification_type = 'participant.invitation.revoked'
  AND notification_in_app_templates.scope_event_id IS NULL
  AND notification_in_app_templates.status = 'published';
