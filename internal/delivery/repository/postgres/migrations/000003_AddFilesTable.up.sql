create table if not exists files
(
    id         uuid primary key,
    name       varchar(255) not null,

    created_at timestamptz  not null default now()
);