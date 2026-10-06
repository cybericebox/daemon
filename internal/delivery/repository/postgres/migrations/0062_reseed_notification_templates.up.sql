-- Replace every saved email and on-site template version, including event-scoped
-- copies. Keep an exact snapshot so the down migration can restore them.
CREATE TABLE notification_template_seed_0062_backup (
    channel text NOT NULL CHECK (channel IN ('email', 'in_app')),
    id uuid NOT NULL,
    row_data jsonb NOT NULL,
    PRIMARY KEY (channel, id)
);

INSERT INTO notification_template_seed_0062_backup (channel, id, row_data)
SELECT 'email', id, to_jsonb(t) FROM notification_email_templates t
UNION ALL
SELECT 'in_app', id, to_jsonb(t) FROM notification_in_app_templates t;

DELETE FROM notification_email_templates;
DELETE FROM notification_in_app_templates;

-- Build the same Lexical rich-text shape that the email editor and renderer use.
-- {{Variable}} becomes a variable node; button URLs remain renderer placeholders.
CREATE FUNCTION notification_seed_0062_rich_text(source text) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE
    paragraph text;
    rest text;
    found text[];
    token text;
    token_at integer;
    children jsonb;
    paragraphs jsonb := '[]'::jsonb;
BEGIN
    FOREACH paragraph IN ARRAY string_to_array(replace(source, '\n\n', E'\n\n'), E'\n\n') LOOP
        rest := paragraph;
        children := '[]'::jsonb;
        LOOP
            found := regexp_match(rest, '\{\{([A-Za-z_][A-Za-z_0-9]*)\}\}');
            EXIT WHEN found IS NULL;
            token := '{{' || found[1] || '}}';
            token_at := strpos(rest, token);
            IF token_at > 1 THEN
                children := children || jsonb_build_array(jsonb_build_object(
                    'type', 'text', 'text', left(rest, token_at - 1), 'format', 0));
            END IF;
            children := children || jsonb_build_array(jsonb_build_object(
                'type', 'variable', 'varName', found[1]));
            rest := substr(rest, token_at + length(token));
        END LOOP;
        IF rest <> '' THEN
            children := children || jsonb_build_array(jsonb_build_object(
                'type', 'text', 'text', rest, 'format', 0));
        END IF;
        paragraphs := paragraphs || jsonb_build_array(jsonb_build_object(
            'type', 'paragraph', 'children', children));
    END LOOP;
    RETURN jsonb_build_object('type', 'rich_text', 'content',
        jsonb_build_object('root', jsonb_build_object('type', 'root', 'children', paragraphs)));
END;
$$;

WITH seeds(notification_type, subject, preheader, message, button_label, button_url) AS (
    VALUES
    ('continue_registration', 'Завершіть реєстрацію в CyberICEBox', 'Ваш обліковий запис майже готовий',
     'Вітаємо, {{Name}}!\n\nЗавершіть реєстрацію, щоб отримати доступ до CyberICEBox.', 'Завершити реєстрацію', '{{RegistrationURL}}'),
    ('password_reset', 'Скидання пароля CyberICEBox', 'Відновіть доступ до облікового запису',
     'Вітаємо, {{Name}}!\n\nМи отримали запит на скидання пароля. Якщо це були не ви, просто проігноруйте лист.', 'Скинути пароль', '{{ResetURL}}'),
    ('email_confirmation', 'Підтвердьте електронну пошту', 'Підтвердіть нову адресу пошти',
     'Вітаємо, {{Name}}!\n\nПідтвердьте нову адресу електронної пошти для свого облікового запису.', 'Підтвердити пошту', '{{ConfirmURL}}'),
    ('user_invitation', 'Вас запросили до CyberICEBox', 'Завершіть налаштування облікового запису',
     'Вас запросили до CyberICEBox.\n\nПрийміть запрошення та налаштуйте свій обліковий запис.', 'Прийняти запрошення', '{{InviteURL}}'),
    ('account_exists', 'Обліковий запис уже існує', 'Для цієї пошти вже є обліковий запис',
     'Вітаємо, {{Name}}!\n\nДля цієї адреси пошти вже створено обліковий запис CyberICEBox. Якщо це були ви, увійдіть до нього. Якщо ні — жодних дій не потрібно.', '', ''),
    ('flag_accepted', 'Прапор завдання «{{.Challenge}}» зараховано', 'Ви отримали {{.Points}} балів',
     'Прапор завдання «{{Challenge}}» зараховано.\n\nВи отримали {{Points}} балів.', '', ''),
    ('event.manager.assigned', 'Доступ до заходу «{{.event_name}}»', 'Вам призначено роль у заході',
     'Вас призначено {{role_name}} заходу «{{event_name}}».\n\nДоступ до заходу надано відповідно до вашої ролі.', '', ''),
    ('participant.approval_registration.submitted', 'Заявку на захід «{{.event_name}}» подано', 'Очікуйте рішення щодо участі',
     'Вашу заявку на участь у заході «{{event_name}}» отримано.\n\nМи повідомимо вас про рішення.', '', ''),
    ('participant.approval_registration.approved', 'Участь у заході «{{.event_name}}» схвалено', 'Вашу заявку схвалено',
     'Вашу заявку на участь у заході «{{event_name}}» схвалено.\n\nВи можете долучитися до заходу.', '', ''),
    ('participant.approval_registration.rejected', 'Заявку на захід «{{.event_name}}» відхилено', 'Рішення щодо вашої заявки',
     'Вашу заявку на участь у заході «{{event_name}}» відхилено.\n\nЯкщо це сталося помилково, зверніться до організаторів заходу.', '', ''),
    ('participant.open_registration.completed', 'Реєстрацію на захід «{{.event_name}}» завершено', 'Ви зареєстровані на захід',
     'Ви успішно зареєструвалися на захід «{{event_name}}».\n\nІнформація про участь з’явиться у вашому обліковому записі.', '', ''),
    ('participant.invitation.sent', 'Запрошення до заходу «{{.event_name}}»', 'Вас запросили взяти участь',
     'Вас запросили взяти участь у заході «{{event_name}}».\n\nПеревірте запрошення у своєму обліковому записі.', '', ''),
    ('participant.invitation.accepted', 'Запрошення до заходу «{{.event_name}}» прийнято', 'Вашу участь підтверджено',
     'Ви прийняли запрошення до заходу «{{event_name}}».\n\nВаша участь підтверджена.', '', ''),
    ('participant.invitation.declined', 'Запрошення до заходу «{{.event_name}}» відхилено', 'Ваше рішення збережено',
     'Ви відхилили запрошення до заходу «{{event_name}}».\n\nВаше рішення збережено.', '', ''),
    ('participant.invitation.revoked', 'Запрошення до заходу «{{.event_name}}» скасовано', 'Запрошення більше не чинне',
     'Ваше запрошення до заходу «{{event_name}}» скасовано.\n\nЯкщо це сталося помилково, зверніться до організаторів заходу.', '', ''),
    ('participant.invitation.expired', 'Термін запрошення до заходу «{{.event_name}}» минув', 'Запрошення більше не чинне',
     'Термін вашого запрошення до заходу «{{event_name}}» минув.\n\nЗверніться до організаторів, якщо бажаєте взяти участь.', '', ''),
    ('participant.enrolled', 'Вас зараховано на захід «{{.event_name}}»', 'Ваша участь підтверджена',
     'Вас зараховано на захід «{{event_name}}».\n\nІнформація про участь доступна у вашому обліковому записі.', '', '')
)
INSERT INTO notification_email_templates
    (id, notification_type, status, subject, preheader, body, styling, published_at)
SELECT ('019b0000-0000-7000-8000-' || lpad(row_number() OVER (ORDER BY notification_type)::text, 12, '0'))::uuid,
       notification_type, 'published', subject, preheader,
       jsonb_build_array(notification_seed_0062_rich_text(message))
         || CASE WHEN button_url <> '' THEN jsonb_build_array(jsonb_build_object(
                'type', 'button', 'label', button_label, 'url', button_url, 'align', 'left'))
            ELSE '[]'::jsonb END
         || jsonb_build_array(jsonb_build_object('type', 'divider'),
                notification_seed_0062_rich_text('CyberICEBox · Автоматичне повідомлення платформи.')),
       '{"font_family":"Arial, sans-serif","text_color":"#292841","text_font_size":"15px","text_line_height":"1.6","heading_color":"#221b54","cta_bg_color":"#221b54","cta_text_color":"#FFFFFF","cta_border_radius":"8px"}'::jsonb,
       now()
FROM seeds;

WITH seeds(notification_type, title, message, icon, tone) AS (
    VALUES
    ('flag_accepted', 'Прапор зараховано', 'Прапор завдання «{{.Challenge}}» зараховано. Ви отримали {{.Points}} балів.', 'trophy', 'success'),
    ('event.manager.assigned', 'Доступ до заходу', 'Вас призначено {{.role_name}} заходу «{{.event_name}}».', 'shield', 'info'),
    ('participant.approval_registration.submitted', 'Заявку на участь подано', 'Вашу заявку на захід «{{.event_name}}» отримано. Очікуйте рішення.', 'mail', 'info'),
    ('participant.approval_registration.approved', 'Заявку на участь схвалено', 'Ви можете брати участь у заході «{{.event_name}}».', 'success', 'success'),
    ('participant.approval_registration.rejected', 'Заявку на участь відхилено', 'Вашу заявку на захід «{{.event_name}}» відхилено.', 'warning', 'warning'),
    ('participant.open_registration.completed', 'Реєстрацію завершено', 'Ви зареєстровані на захід «{{.event_name}}».', 'success', 'success'),
    ('participant.invitation.sent', 'Запрошення до заходу', 'Вас запросили взяти участь у заході «{{.event_name}}».', 'mail', 'info'),
    ('participant.invitation.accepted', 'Запрошення прийнято', 'Ви прийняли запрошення до заходу «{{.event_name}}».', 'success', 'success'),
    ('participant.invitation.declined', 'Запрошення відхилено', 'Ви відхилили запрошення до заходу «{{.event_name}}».', 'warning', 'warning'),
    ('participant.invitation.revoked', 'Запрошення скасовано', 'Ваше запрошення до заходу «{{.event_name}}» скасовано.', 'warning', 'warning'),
    ('participant.invitation.expired', 'Термін запрошення минув', 'Термін вашого запрошення до заходу «{{.event_name}}» минув.', 'warning', 'warning'),
    ('participant.enrolled', 'Участь підтверджено', 'Вас зараховано на захід «{{.event_name}}».', 'success', 'success')
)
INSERT INTO notification_in_app_templates
    (id, notification_type, status, title, body, link, icon, tone, surface,
     auto_dismiss_ms, actions, dismissible, published_at)
SELECT ('019b0000-0000-7000-8001-' || lpad(row_number() OVER (ORDER BY notification_type)::text, 12, '0'))::uuid,
       notification_type, 'published', title, message, '', icon, tone, 'inbox',
       5000, '[]'::jsonb, true, now()
FROM seeds;

DROP FUNCTION notification_seed_0062_rich_text(text);
