create table if not exists temporal_codes
(
    id         uuid primary key,
    expired_at timestamptz not null,
    code_type  integer     not null,
    data       jsonb       not null,

    created_at timestamptz not null default now()
);