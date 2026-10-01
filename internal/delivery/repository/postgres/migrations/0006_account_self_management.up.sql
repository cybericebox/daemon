alter table users
    add column deleted_at timestamptz;

-- Seed the default active email-confirmation template (used by email change) so
-- the confirmation mail resolves a template out of the box. Admins can edit it.
insert into notification_email_templates (id, notification_type, status, subject, preheader, body)
values ('01940000-0000-7000-8000-000000000006', 'email_confirmation', 'active',
        'Confirm your new email',
        'Confirm your email change',
        '<p>Hi {{.Name}},</p><p>Click <a href="{{.ConfirmURL}}">here</a> to confirm your new email address.</p>');
-- NOTE: the var name {{.ConfirmURL}} MUST match the existing EmailConfirmationPayload's
-- `var:"ConfirmURL"` tag (internal/model/notification/types/payloads/email_confirmation.go).
