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


