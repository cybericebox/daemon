insert into platform_settings (key, value)
values ('account_exists_template',
        '{
          "subject": "Спроба зареєструвати існуючий обліковий запис",
          "body": "<!DOCTYPE html><html lang=\"uk\"><body><h3>Вітаємо, {{.Username}}!</h3><p>Цей лист було відправлено на запит про реєстрацію вже існуючого облікового запису</p><p>Якщо виникла помилка, проігноруйте цей лист.</p></body></html>"
        }'),
       ('continue_registration_template',
        '{
          "subject": "Продовження реєстрації",
          "body": "<!DOCTYPE html><html lang=\"uk\"><body><h3>Вітаємо!</h3><p>Цей лист було відправлено на запит про підтвердження адреси електронної пошти.</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб підтвердити адресу електронної пошти перейдіть за наступним посиланням:</p><br/><span><a href=\"{{.Link}}\">{{.Link}}</a></span></body></html>"
        }'),
       ('email_confirmation_template',
        '{
          "subject": " Підтвердження адреси електронної пошти",
          "body": "<!DOCTYPE html><html lang=\"uk\"><body><h3>Вітаємо, {{.Username}}!</h3><p>Цей лист було відправлено на запит про підтвердження адреси електронної пошти.</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб підтвердити адресу електронної пошти перейдіть за наступним посиланням:</p><br/><span><a href=\"{{.Link}}\">{{.Link}}</a></span></body></html>"
        }'),
       ('password_resetting_template',
        '{
          "subject": "Відновлення паролю",
          "body": "<!DOCTYPE html><html lang=\"uk\"><body><h3>Вітаємо, {{.Username}}!</h3><p>Цей лист було відправлено на запит про відновлення паролю на пратформі Cyber ICE Box</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб відновити пароль перейдіть за наступним посиланням:</p><br/><span><a href=\"{{.Link}}\">{{.Link}}</a></span></body></html>"
        }'),
       ('invitation_to_registration_template',
        '{
          "subject": "Запрошення на реєстрацію на платформі Cyber ICE Box",
          "body": "<!DOCTYPE html><html lang=\"uk\"><body><h3>Вітаємо!</h3><p>Вас запрошено на реєстрацію на платформі Cyber ICE Box.</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб зареєструватися перейдіть за наступним посиланням:</p><br/><span><a href=\"{{.Link}}\">{{.Link}}</a></span></body></html>"
        }')
ON CONFLICT DO NOTHING;
