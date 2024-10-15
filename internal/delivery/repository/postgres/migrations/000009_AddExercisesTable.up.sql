create table if not exists exercises
(
    id          uuid primary key,
    category_id uuid         not null references exercise_categories (id) on delete no action,

    name varchar(255) not null unique,
    description text         not null,
    data        jsonb        not null,

    updated_at  timestamptz,
    updated_by  uuid         references users (id) on delete set null,

    created_at  timestamptz  not null default now()
);



