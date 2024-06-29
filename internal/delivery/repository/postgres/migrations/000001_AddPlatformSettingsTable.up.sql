create table if not exists platform_settings
(
    id         serial primary key,

    type       varchar(255) not null,
    key        varchar(255) not null,
    value      text         not null,

    created_at timestamptz  not null default now()
);