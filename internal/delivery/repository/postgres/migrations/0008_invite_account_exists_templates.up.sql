insert into notification_email_templates (id, notification_type, status, subject, preheader, body)
values ('01940000-0000-7000-8000-000000000008', 'user_invitation', 'active',
        'You have been invited',
        'Complete your account setup',
        '<p>You have been invited. Click <a href="{{.InviteURL}}">here</a> to set up your account.</p>'),
       ('01940000-0000-7000-8000-000000000009', 'account_exists', 'active',
        'Account already exists',
        'Someone tried to register with your email',
        '<p>Hi {{.Name}},</p><p>Someone tried to register an account with your email. If this was you, sign in instead. If not, you can ignore this message.</p>');
