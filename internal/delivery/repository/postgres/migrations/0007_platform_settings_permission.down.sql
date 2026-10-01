alter table app_settings
    add column read_access smallint not null default 0;
update app_settings
set read_access = case when required_permission = '' then 4 else 1 end;
alter table app_settings drop column required_permission;
