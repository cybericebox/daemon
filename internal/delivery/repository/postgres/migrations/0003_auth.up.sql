create table users
(
    id              uuid primary key,
    email           varchar(255) not null unique,
    first_name      varchar(255) not null default '',
    last_name       varchar(255) not null default '',
    hashed_password varchar(72),
    picture         varchar(255) not null default '',
    role            varchar(64)  not null default 'user',
    status          varchar(16)  not null default 'active',
    email_confirmed boolean      not null default false,
    last_seen       timestamptz,
    updated_at      timestamptz,
    updated_by      uuid         references users (id) on delete set null,
    created_at      timestamptz  not null default now()
);

create table sessions
(
    id         uuid primary key,
    user_id    uuid        not null references users (id) on delete cascade,
    expires_at timestamptz not null,
    last_seen  timestamptz not null default now(),
    metadata   jsonb       not null default '{}'
);

create index sessions_user_id_idx on sessions (user_id);
create index sessions_expires_at_idx on sessions (expires_at);
