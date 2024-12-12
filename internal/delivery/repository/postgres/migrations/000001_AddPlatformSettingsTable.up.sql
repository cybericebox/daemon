create table if not exists platform_settings
(
    key   varchar(255) primary key,
    value jsonb not null,

    created_at timestamptz  not null default now()
);
