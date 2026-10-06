delete
from notification_email_templates
where notification_type in ('continue_registration', 'password_reset');
drop table if exists temporal_codes;
alter table users drop column if exists tos_version;
alter table users drop column if exists tos_accepted_at;
