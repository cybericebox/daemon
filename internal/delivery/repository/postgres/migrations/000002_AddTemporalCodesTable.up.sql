create table if not exists temporal_codes
(
    id         uuid primary key,
    expired_at timestamptz  not null,
    code_type  integer      not null,
    v0         varchar(255) not null,
    v1         varchar(255) not null,
    v2         varchar(255) not null,

    created_at timestamptz  not null default now()
);