-- name: GetEmailTemplateSubject :one
select value
from platform_settings
where type = 'email_template_subject'
  and key = $1;

-- name: GetEmailTemplateBody :one
select value
from platform_settings
where type = 'email_template_body'
  and key = $1;

-- name: UpdateEmailTemplateSubject :execrows
update platform_settings
set value      = $2,
    updated_at = now(),
    updated_by = $3
where type = 'email_template_subject'
  and key = $1;

-- name: UpdateEmailTemplateBody :execrows
update platform_settings
set value      = $2,
    updated_at = now(),
    updated_by = $3
where type = 'email_template_body'
  and key = $1;


