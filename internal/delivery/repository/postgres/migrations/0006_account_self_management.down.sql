delete
from notification_email_templates
where id = '01940000-0000-7000-8000-000000000006';
alter table users drop column if exists deleted_at;
