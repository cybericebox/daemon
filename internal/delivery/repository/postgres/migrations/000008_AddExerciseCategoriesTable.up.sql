create table if not exists exercise_categories
(
    id          uuid primary key,

    name        varchar(255) not null unique,
    description text         not null,

    updated_at  timestamptz,
    updated_by  uuid         references users (id) on delete set null,

    created_at  timestamptz  not null default now()
);