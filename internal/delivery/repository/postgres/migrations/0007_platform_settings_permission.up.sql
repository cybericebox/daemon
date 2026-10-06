alter table app_settings
    add column required_permission text not null default '';

-- Carry old numeric read_access thresholds (smaller = more privileged) into a
-- permission: admin-or-tighter rows require platform.settings.read; the rest
-- become publicly readable (empty). Admins can refine per row afterward.
update app_settings
set required_permission = case when read_access <= 2 then 'platform.settings.read' else '' end;

alter table app_settings drop column read_access;
