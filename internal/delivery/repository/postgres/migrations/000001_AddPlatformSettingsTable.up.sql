create table if not exists platform_settings
(
    id         serial primary key,

    type       varchar(255) not null,
    key        varchar(255) not null,
    value      text         not null,

    created_at timestamptz  not null default now()
);

create unique index if not exists platform_settings_type_key_index on platform_settings (type, key);
