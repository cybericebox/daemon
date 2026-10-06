-- Rewrites the seeded in-app (inbox) notification copy: short titles that match
-- the email headings, one sentence, one action link where it helps (the event
-- site, or the invitation). The link now uses the {{.name}} form: the old
-- {{event_url}} is a function call for text/template and failed to render.
-- Source of truth: internal/model/notification/inappdefaults.
--
-- Only untouched rows are replaced: platform scope, published, written by a
-- migration (updated_by_user_id IS NULL) and with no other row of the type.
-- Icon, tone and the other settings stay as seeded. Replaced rows are kept in
-- a backup table for the down migration.
CREATE TABLE notification_template_seed_0128_backup (
    id       uuid PRIMARY KEY,
    row_data jsonb NOT NULL
);

CREATE TEMP TABLE untouched_0128 AS
SELECT t.id, t.notification_type
FROM notification_in_app_templates t
WHERE t.scope_event_id IS NULL
  AND t.status = 'published'
  AND t.updated_by_user_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM notification_in_app_templates o
                  WHERE o.notification_type = t.notification_type AND o.id <> t.id);

INSERT INTO notification_template_seed_0128_backup (id, row_data)
SELECT t.id, to_jsonb(t)
FROM notification_in_app_templates t
WHERE t.id IN (SELECT id FROM untouched_0128);

UPDATE notification_in_app_templates
SET title = $s$Прапор зараховано$s$, body = $s$«{{.Challenge}}»: {{.Points}} балів.$s$, link = $s$$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'flag_accepted');

UPDATE notification_in_app_templates
SET title = $s$Вам надано доступ до заходу$s$, body = $s$Вас призначено {{.role_name}} заходу «{{.event_name}}».$s$, link = $s$$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'event.manager.assigned');

UPDATE notification_in_app_templates
SET title = $s$Лабораторія не працює$s$, body = $s$Команда «{{.team_name}}», захід «{{.event_name}}». Перевірте лабораторію на сторінці «Лабораторії».$s$, link = $s$$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'event.lab.failed');

UPDATE notification_in_app_templates
SET title = $s$Заявку подано$s$, body = $s$Заявку на участь у заході «{{.event_name}}» отримано. Очікуйте рішення.$s$, link = $s$$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.approval_registration.submitted');

UPDATE notification_in_app_templates
SET title = $s$Участь схвалено$s$, body = $s$Заявку на участь у заході «{{.event_name}}» схвалено.$s$, link = $s${{.event_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.approval_registration.approved');

UPDATE notification_in_app_templates
SET title = $s$Заявку відхилено$s$, body = $s$Заявку на участь у заході «{{.event_name}}» відхилено.$s$, link = $s$$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.approval_registration.rejected');

UPDATE notification_in_app_templates
SET title = $s$Ви зареєстровані$s$, body = $s$Ви зареєстровані на захід «{{.event_name}}».$s$, link = $s${{.event_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.open_registration.completed');

UPDATE notification_in_app_templates
SET title = $s$Вас запрошено до заходу$s$, body = $s$Вас запрошено до заходу «{{.event_name}}».$s$, link = $s${{.invite_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.invitation.sent');

UPDATE notification_in_app_templates
SET title = $s$Запрошення до команди$s$, body = $s$Вас запрошено до команди «{{.team_name}}» на заході «{{.event_name}}».$s$, link = $s${{.invite_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.team_invitation.sent');

UPDATE notification_in_app_templates
SET title = $s$Запрошення прийнято$s$, body = $s$Ви прийняли запрошення до заходу «{{.event_name}}».$s$, link = $s${{.event_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.invitation.accepted');

UPDATE notification_in_app_templates
SET title = $s$Запрошення скасовано$s$, body = $s$Запрошення до заходу «{{.event_name}}» скасовано.$s$, link = $s$$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.invitation.revoked');

UPDATE notification_in_app_templates
SET title = $s$Термін запрошення минув$s$, body = $s$Запрошення до заходу «{{.event_name}}» більше не чинне.$s$, link = $s$$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.invitation.expired');

UPDATE notification_in_app_templates
SET title = $s$Захід скоро почнеться$s$, body = $s$Захід «{{.event_name}}» починається {{.start_at}} (за київським часом).$s$, link = $s${{.event_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.event.start_reminder');

UPDATE notification_in_app_templates
SET title = $s$Захід завершено$s$, body = $s$Дякуємо за участь у заході «{{.event_name}}»!$s$, link = $s${{.event_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.event.finished');

UPDATE notification_in_app_templates
SET title = $s$Підсумки відкрито$s$, body = $s$Результати заходу «{{.event_name}}» уже доступні.$s$, link = $s${{.event_url}}$s$, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0128 WHERE notification_type = 'participant.event.results_published');

DROP TABLE untouched_0128;
