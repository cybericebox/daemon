-- Rewrites the seeded email templates: new copy and blocks (logo, heading,
-- facts card, one action button, no signature; the platform footer is added on
-- send). Source of truth: internal/model/notification/emaildefaults.
--
-- Only untouched rows are replaced: platform scope, published, written by a
-- migration (updated_by_user_id IS NULL) and with no other row of the type (an
-- admin edit leaves a draft, or an unpublished row and a row with an author).
-- Edited templates keep their content. The two seeded rows of types the code no
-- longer registers (participant.enrolled, participant.invitation.declined) are
-- deleted. Replaced and deleted rows are kept in a backup table for the down
-- migration.
CREATE TABLE notification_template_seed_0127_backup (
    id      uuid PRIMARY KEY,
    row_data jsonb NOT NULL
);

CREATE TEMP TABLE untouched_0127 AS
SELECT t.id, t.notification_type
FROM notification_email_templates t
WHERE t.scope_event_id IS NULL
  AND t.status = 'published'
  AND t.updated_by_user_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM notification_email_templates o
                  WHERE o.notification_type = t.notification_type AND o.id <> t.id);

INSERT INTO notification_template_seed_0127_backup (id, row_data)
SELECT t.id, to_jsonb(t)
FROM notification_email_templates t
WHERE t.id IN (SELECT id FROM untouched_0127)
   OR t.notification_type IN ('participant.enrolled', 'participant.invitation.declined');

DELETE FROM notification_email_templates
WHERE notification_type IN ('participant.enrolled', 'participant.invitation.declined');

UPDATE notification_email_templates
SET subject = $s$Завершіть реєстрацію в Cyber ICE Box$s$, preheader = $s$Ще один крок, і обліковий запис готовий$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Завершіть реєстрацію","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вітаємо, ","type":"text"},{"type":"variable","varName":"Name"},{"format":0,"text":"! Ще один крок, і ви зможете користуватися Cyber ICE Box.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"align":"left","label":"Завершити реєстрацію","type":"button","url":"{{RegistrationURL}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Кнопка не відкривається? Скопіюйте це посилання в браузер:","type":"text"}],"type":"paragraph"},{"children":[{"children":[{"type":"variable","varName":"RegistrationURL"}],"type":"link","url":"{{RegistrationURL}}"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Якщо ви не реєструвалися, просто проігноруйте цей лист.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'continue_registration');

UPDATE notification_email_templates
SET subject = $s$Скидання пароля Cyber ICE Box$s$, preheader = $s$Відновіть доступ до облікового запису$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Скидання пароля","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вітаємо, ","type":"text"},{"type":"variable","varName":"Name"},{"format":0,"text":"! Ми отримали запит на скидання пароля до вашого облікового запису.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"align":"left","label":"Скинути пароль","type":"button","url":"{{ResetURL}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Кнопка не відкривається? Скопіюйте це посилання в браузер:","type":"text"}],"type":"paragraph"},{"children":[{"children":[{"type":"variable","varName":"ResetURL"}],"type":"link","url":"{{ResetURL}}"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Якщо це були не ви, нічого не робіть: пароль залишиться попереднім.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'password_reset');

UPDATE notification_email_templates
SET subject = $s$Підтвердьте електронну пошту$s$, preheader = $s$Підтвердіть нову адресу пошти$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Підтвердіть електронну пошту","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вітаємо, ","type":"text"},{"type":"variable","varName":"Name"},{"format":0,"text":"! Підтвердьте цю адресу, щоб пов’язати її з обліковим записом Cyber ICE Box.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"align":"left","label":"Підтвердити пошту","type":"button","url":"{{ConfirmURL}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Кнопка не відкривається? Скопіюйте це посилання в браузер:","type":"text"}],"type":"paragraph"},{"children":[{"children":[{"type":"variable","varName":"ConfirmURL"}],"type":"link","url":"{{ConfirmURL}}"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Якщо ви цього не робили, проігноруйте лист.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'email_confirmation');

UPDATE notification_email_templates
SET subject = $s$Вас запрошено до Cyber ICE Box$s$, preheader = $s$Завершіть налаштування облікового запису$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Вас запрошено до Cyber ICE Box","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Прийміть запрошення та налаштуйте свій обліковий запис.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"align":"left","label":"Прийняти запрошення","type":"button","url":"{{InviteURL}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Кнопка не відкривається? Скопіюйте це посилання в браузер:","type":"text"}],"type":"paragraph"},{"children":[{"children":[{"type":"variable","varName":"InviteURL"}],"type":"link","url":"{{InviteURL}}"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'user_invitation');

UPDATE notification_email_templates
SET subject = $s$Обліковий запис уже існує$s$, preheader = $s$Для цієї пошти вже є обліковий запис$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Обліковий запис уже існує","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вітаємо, ","type":"text"},{"type":"variable","varName":"Name"},{"format":0,"text":"! Для цієї адреси вже є обліковий запис Cyber ICE Box. Якщо це були ви, просто увійдіть до нього.","type":"text"}],"type":"paragraph"},{"children":[{"format":0,"text":"Якщо ви не реєструвалися, жодних дій не потрібно.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'account_exists');

UPDATE notification_email_templates
SET subject = $s$Ваш обліковий запис Cyber ICE Box буде видалено$s$, preheader = $s$Увійдіть, щоб зберегти обліковий запис$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Обліковий запис буде видалено","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вітаємо, ","type":"text"},{"type":"variable","varName":"Name"},{"format":0,"text":"! Ви давно не входили до Cyber ICE Box. Відповідно до Політики конфіденційності ми видалимо обліковий запис і пов’язані з ним персональні дані.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Дата видалення","value":"{{DeletionDate}}"}],"type":"facts"},{"align":"left","label":"Увійти й зберегти обліковий запис","type":"button","url":"{{SignInURL}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Результати змагань залишаться в історії заходів у знеособленому вигляді.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'account_inactivity_warning');

UPDATE notification_email_templates
SET subject = $s$Прапор завдання «{{.Challenge}}» зараховано$s$, preheader = $s$Ви отримали {{.Points}} балів$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Прапор зараховано","type":"text"}],"tag":"h1","type":"heading"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Завдання","value":"{{Challenge}}"},{"label":"Бали","value":"{{Points}}"}],"type":"facts"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'flag_accepted');

UPDATE notification_email_templates
SET subject = $s$Доступ до заходу «{{.event_name}}»$s$, preheader = $s$Вам призначено роль у заході$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Вам надано доступ до заходу","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вас призначено ","type":"text"},{"type":"variable","varName":"role_name"},{"format":0,"text":" заходу «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"».","type":"text"}],"type":"paragraph"},{"children":[{"format":0,"text":"Доступ відповідає вашій ролі.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'event.manager.assigned');

UPDATE notification_email_templates
SET subject = $s$Лабораторія не працює: команда «{{.team_name}}»$s$, preheader = $s$Лабораторія команди «{{.team_name}}» не працює$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Лабораторія не працює","type":"text"}],"tag":"h1","type":"heading"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"},{"label":"Команда","value":"{{team_name}}"},{"label":"Причина","value":"{{reason}}"}],"type":"facts"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Перевірте лабораторію команди на сторінці «Лабораторії» керування заходом.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'event.lab.failed');

UPDATE notification_email_templates
SET subject = $s$Заявку на захід «{{.event_name}}» подано$s$, preheader = $s$Очікуйте рішення щодо участі$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Заявку подано","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Ми отримали вашу заявку на участь у заході «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"». Організатори розглянуть її та повідомлять про рішення.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"},{"label":"Статус","value":"На розгляді"}],"type":"facts"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.approval_registration.submitted');

UPDATE notification_email_templates
SET subject = $s$Участь у заході «{{.event_name}}» схвалено$s$, preheader = $s$Вашу заявку схвалено$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Участь схвалено","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вашу заявку на участь у заході «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"» схвалено. Ви можете долучитися до заходу.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"},{"label":"Статус","value":"Схвалено"}],"type":"facts"},{"align":"left","label":"Перейти до заходу","type":"button","url":"{{event_url}}"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.approval_registration.approved');

UPDATE notification_email_templates
SET subject = $s$Заявку на захід «{{.event_name}}» відхилено$s$, preheader = $s$Рішення щодо вашої заявки$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Заявку відхилено","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"На жаль, вашу заявку на участь у заході «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"» відхилено.","type":"text"}],"type":"paragraph"},{"children":[{"format":0,"text":"Якщо це сталося помилково, зверніться до організаторів заходу.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.approval_registration.rejected');

UPDATE notification_email_templates
SET subject = $s$Реєстрацію на захід «{{.event_name}}» завершено$s$, preheader = $s$Ви зареєстровані на захід$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Ви зареєстровані","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Ви успішно зареєструвалися на захід «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"». Інформація про участь з’явиться у вашому обліковому записі.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"}],"type":"facts"},{"align":"left","label":"Перейти до заходу","type":"button","url":"{{event_url}}"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.open_registration.completed');

UPDATE notification_email_templates
SET subject = $s$Запрошення до заходу «{{.event_name}}»$s$, preheader = $s$Вас запросили взяти участь$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Вас запрошено до заходу","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Організатори запрошують вас узяти участь у заході «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"».","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"}],"type":"facts"},{"align":"left","label":"Прийняти запрошення","type":"button","url":"{{invite_url}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Кнопка не відкривається? Скопіюйте це посилання в браузер:","type":"text"}],"type":"paragraph"},{"children":[{"children":[{"type":"variable","varName":"invite_url"}],"type":"link","url":"{{invite_url}}"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.invitation.sent');

UPDATE notification_email_templates
SET subject = $s$Запрошення до команди «{{.team_name}}»$s$, preheader = $s$Вас запросили до команди на заході$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Запрошення до команди","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Вас запрошують до команди «","type":"text"},{"type":"variable","varName":"team_name"},{"format":0,"text":"» на заході «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"».","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"},{"label":"Команда","value":"{{team_name}}"}],"type":"facts"},{"align":"left","label":"Прийняти запрошення","type":"button","url":"{{invite_url}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Кнопка не відкривається? Скопіюйте це посилання в браузер:","type":"text"}],"type":"paragraph"},{"children":[{"children":[{"type":"variable","varName":"invite_url"}],"type":"link","url":"{{invite_url}}"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Після прийняття запрошення команда з’явиться на ","type":"text"},{"children":[{"format":0,"text":"сторінці участі","type":"text"}],"type":"link","url":"{{team_url}}"},{"format":0,"text":".","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.team_invitation.sent');

UPDATE notification_email_templates
SET subject = $s$Запрошення до заходу «{{.event_name}}» прийнято$s$, preheader = $s$Вашу участь підтверджено$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Запрошення прийнято","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Ви прийняли запрошення до заходу «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"». Вашу участь підтверджено.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"},{"label":"Статус","value":"Участь підтверджено"}],"type":"facts"},{"align":"left","label":"Перейти до заходу","type":"button","url":"{{event_url}}"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.invitation.accepted');

UPDATE notification_email_templates
SET subject = $s$Запрошення до заходу «{{.event_name}}» скасовано$s$, preheader = $s$Запрошення більше не чинне$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Запрошення скасовано","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Запрошення до заходу «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"» скасовано.","type":"text"}],"type":"paragraph"},{"children":[{"format":0,"text":"Якщо це сталося помилково, зверніться до організаторів заходу.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.invitation.revoked');

UPDATE notification_email_templates
SET subject = $s$Термін запрошення до заходу «{{.event_name}}» минув$s$, preheader = $s$Запрошення більше не чинне$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Термін запрошення минув","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Запрошення до заходу «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"» більше не чинне.","type":"text"}],"type":"paragraph"},{"children":[{"format":0,"text":"Якщо хочете взяти участь, зверніться до організаторів.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.invitation.expired');

UPDATE notification_email_templates
SET subject = $s$Захід «{{.event_name}}» починається {{.start_at}}$s$, preheader = $s$Нагадування про старт$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Захід скоро почнеться","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Нагадуємо про захід «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"».","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"},{"label":"Початок (за київським часом)","value":"{{start_at}}"}],"type":"facts"},{"align":"left","label":"Перейти до заходу","type":"button","url":"{{event_url}}"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Заздалегідь перевірте, що можете увійти до облікового запису.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.event.start_reminder');

UPDATE notification_email_templates
SET subject = $s$Захід «{{.event_name}}» завершено$s$, preheader = $s$Дякуємо за участь$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Захід завершено","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Дякуємо за участь у заході «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"»!","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"items":[{"label":"Захід","value":"{{event_name}}"},{"label":"Завершення (за київським часом)","value":"{{finish_at}}"}],"type":"facts"},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Результати з’являться на сайті заходу, щойно організатори їх відкриють.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"align":"left","label":"Перейти на сайт заходу","type":"button","url":"{{event_url}}"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.event.finished');

UPDATE notification_email_templates
SET subject = $s$Підсумки заходу «{{.event_name}}» відкрито$s$, preheader = $s$Результати вже доступні$s$,
    body = $j$[{"align":"left","type":"logo","width_px":44},{"content":{"root":{"children":[{"children":[{"format":0,"text":"Підсумки відкрито","type":"text"}],"tag":"h1","type":"heading"},{"children":[{"format":0,"text":"Результати заходу «","type":"text"},{"type":"variable","varName":"event_name"},{"format":0,"text":"» уже доступні на сайті заходу.","type":"text"}],"type":"paragraph"}],"type":"root"}},"type":"rich_text"},{"align":"left","label":"Переглянути результати","type":"button","url":"{{event_url}}"}]$j$::jsonb, styling = '{}'::jsonb, updated_at = now()
WHERE id IN (SELECT id FROM untouched_0127 WHERE notification_type = 'participant.event.results_published');

DROP TABLE untouched_0127;
