alter table users
    add column tos_accepted_at timestamptz;
alter table users
    add column tos_version int;

create table temporal_codes
(
    id         uuid primary key,
    code       text        not null unique,
    type       int         not null,
    data       jsonb       not null default '{}',
    expires_at timestamptz not null,
    created_at timestamptz not null default now()
);

create index temporal_codes_code_idx on temporal_codes (code);
create index temporal_codes_expires_at_idx on temporal_codes (expires_at);

-- Seed default active email templates so registration/reset emails resolve a
-- template out of the box (admins can edit/replace via the template API).
insert into notification_email_templates (id, notification_type, status, subject, preheader, body)
values (gen_random_uuid(), 'continue_registration', 'active',
        'Complete your registration',
        'Finish setting up your account',
        '<p>Hello,</p><p>Click <a href="{{.RegistrationURL}}">here</a> to finish creating your account.</p>'),
       (gen_random_uuid(), 'password_reset', 'active',
        'Reset your password',
        'Password reset request',
        '<p>Hi {{.Name}},</p><p>Click <a href="{{.ResetURL}}">here</a> to reset your password. If you did not request this, ignore this email.</p>');
