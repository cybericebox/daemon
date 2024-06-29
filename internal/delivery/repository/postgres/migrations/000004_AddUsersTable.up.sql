create table if not exists users
(
    id              uuid primary key,
    google_id       varchar(255) unique,

    email           varchar(255) not null unique,
    name            varchar(255) not null,
    hashed_password varchar(64)  not null,
    picture         varchar(255) not null,

    role            varchar(64)  not null,

    last_seen       timestamptz  not null default now(),

    updated_at      timestamptz,
    updated_by      uuid         references users (id) on delete set null,

    created_at      timestamptz  not null default now()
);