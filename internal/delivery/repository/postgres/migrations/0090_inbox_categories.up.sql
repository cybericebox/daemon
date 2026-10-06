-- Inbox categories (INBOX-DESIGN §2, §4.1): every in-app row carries its
-- notification type and the server-computed tab (requests | personal |
-- activity). Requests are action-required and share a subject_ref across all
-- recipients, so resolving the object closes every copy at once.
ALTER TABLE in_app_notifications
    ADD COLUMN notification_type text    NOT NULL DEFAULT '',
    ADD COLUMN category          text    NOT NULL DEFAULT 'personal'
        CONSTRAINT in_app_notifications_category_check
            CHECK (category IN ('requests', 'personal', 'activity')),
    ADD COLUMN action_required   boolean NOT NULL DEFAULT false,
    ADD COLUMN subject_ref       text,
    ADD COLUMN resolved_at       timestamptz,
    ADD COLUMN resolution        text
        CONSTRAINT in_app_notifications_resolution_check
            CHECK (resolution IN ('approved', 'rejected', 'fixed', 'resolved')),
    ADD COLUMN resolved_by       uuid,
    ADD CONSTRAINT in_app_notifications_action_category_check
        CHECK (NOT action_required OR category = 'requests'),
    ADD CONSTRAINT in_app_notifications_resolved_check
        CHECK ((resolved_at IS NULL) = (resolution IS NULL));

-- Best-effort backfill: the type comes from the nearest earlier in-app
-- dispatch of the same recipient and scope. Legacy rows never become
-- action-required (they have no subject to resolve).
UPDATE in_app_notifications n
SET notification_type = COALESCE((
    SELECT d.notification_type
    FROM notification_dispatches d
    JOIN notification_dispatch_targets t ON t.dispatch_id = d.id AND t.channel = 'in_app'
    WHERE d.recipient_user_id = n.user_id
      AND d.scope_event_id IS NOT DISTINCT FROM n.scope_event_id
      AND d.created_at BETWEEN n.created_at - interval '1 hour' AND n.created_at + interval '1 second'
    ORDER BY d.created_at DESC
    LIMIT 1), '');

UPDATE in_app_notifications
SET category = CASE
        WHEN notification_type = 'event.lab.failed' THEN 'requests'
        WHEN notification_type IN ('participant.event.start_reminder', 'participant.event.finished') THEN 'activity'
        ELSE 'personal'
    END
WHERE notification_type <> '';

CREATE INDEX in_app_notifications_user_category_idx
    ON in_app_notifications (user_id, category, created_at DESC, id DESC);
CREATE INDEX in_app_notifications_open_request_idx
    ON in_app_notifications (user_id)
    WHERE action_required AND resolved_at IS NULL;
CREATE INDEX in_app_notifications_subject_ref_idx
    ON in_app_notifications (subject_ref)
    WHERE subject_ref IS NOT NULL AND resolved_at IS NULL;

-- Inbox-only notification types: the managers' copy of a registration
-- application and the exercise catalog proposal flow. Platform templates,
-- styled like the manager assignment message.
INSERT INTO notification_in_app_templates
    (id, notification_type, status, title, body, link, icon, tone, surface,
     auto_dismiss_ms, actions, dismissible, published_at)
SELECT gen_random_uuid(), seed.notification_type, 'published', seed.title, seed.body,
       '', seed.icon, seed.tone, surface, auto_dismiss_ms, actions, dismissible, now()
FROM notification_in_app_templates,
     (VALUES
        ('event.application.submitted', 'Нова заявка',
         '{{.applicant_name}} подає заявку на участь у заході «{{.event_name}}».', 'mail', 'info'),
        ('exercise.proposal.submitted', 'Пропозиція вправи',
         '{{.proposer_name}} пропонує вправу «{{.exercise_name}}» до каталогу.', 'mail', 'info'),
        ('exercise.proposal.approved', 'Пропозицію схвалено',
         'Вправу «{{.exercise_name}}» додано до каталогу.', 'success', 'success'),
        ('exercise.proposal.rejected', 'Пропозицію відхилено',
         'Пропозицію вправи «{{.exercise_name}}» відхилено.', 'warning', 'warning')
     ) AS seed(notification_type, title, body, icon, tone)
WHERE notification_in_app_templates.notification_type = 'event.manager.assigned'
  AND notification_in_app_templates.scope_event_id IS NULL
  AND notification_in_app_templates.status = 'published';
