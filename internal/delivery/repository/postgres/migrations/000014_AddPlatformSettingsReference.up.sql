alter table platform_settings
    add column updated_at timestamptz,
    add column updated_by uuid references users (id) on delete set null;
