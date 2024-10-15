insert into platform_settings (type, key, value)
values
-- Email letters for the platform
-- Account Exists
('email_template_subject', 'account_exists_template', 'Спроба зареєструвати існуючий обліковий запис'),
('email_template_body', 'account_exists_template',
 '<!DOCTYPE html><html lang="uk"><body><h3>Вітаємо, {{.Username}}!</h3><p>Цей лист було відправлено на запит про реєстрацію вже існуючого облікового запису</p><p>Якщо виникла помилка, проігноруйте цей лист.</p></body></html>'),
-- Continue Registration
('email_template_subject', 'continue_registration_template', 'Продовження реєстрації'),
('email_template_body', 'continue_registration_template',
 '<!DOCTYPE html><html lang="uk"><body><h3>Вітаємо!</h3><p>Цей лист було відправлено на запит про підтвердження адреси електронної пошти.</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб підтвердити адресу електронної пошти перейдіть за наступним посиланням:</p><br/><span><a href="{{.Link}}">{{.Link}}</a></span></body></html>'),
-- Email Confirmation
('email_template_subject', 'email_confirmation_template', ' Підтвердження електронної пошти'),
('email_template_body', 'email_confirmation_template',
 '<!DOCTYPE html><html lang="uk"><body><h3>Вітаємо, {{.Username}}!</h3><p>Цей лист було відправлено на запит про підтвердження адреси електронної пошти.</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб підтвердити адресу електронної пошти перейдіть за наступним посиланням:</p><br/><span><a href="{{.Link}}">{{.Link}}</a></span></body></html>'),
-- Password Resetting
('email_template_subject', 'password_resetting_template', 'Скидання пароля'),
('email_template_body', 'password_resetting_template',
 '<!DOCTYPE html><html lang="uk"><body><h3>Вітаємо, {{.Username}}!</h3><p>Цей лист було відправлено на запит про відновлення паролю на пратформі Cyber ICE Box</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб відновити пароль перейдіть за наступним посиланням:</p><br/><span><a href="{{.Link}}">{{.Link}}</a></span></body></html>'),
-- Invitation to registration
('email_template_subject', 'invitation_to_registration_template',
 'Запрошення на реєстрацію на платформі Cyber ICE Box'),
('email_template_body', 'invitation_to_registration_template',
 '<!DOCTYPE html><html lang="uk"><body><h3>Вітаємо!</h3><p>Вас запрошено на реєстрацію на платформі Cyber ICE Box.</p><p>Якщо виникла помилка, проігноруйте цей лист.</p><p>Щоб зареєструватися перейдіть за наступним посиланням:</p><br/><span><a href="{{.Link}}">{{.Link}}</a></span></body></html>')
ON CONFLICT DO NOTHING;