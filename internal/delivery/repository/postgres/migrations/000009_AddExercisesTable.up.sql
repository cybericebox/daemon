create table if not exists exercises
(
    id          uuid primary key,
    category_id uuid         not null references exercise_categories (id) on delete no action,

    name        varchar(255) not null,
    description text         not null,
    data        jsonb        not null,

    updated_at  timestamptz,
    updated_by  uuid         references users (id) on delete set null,

    created_at  timestamptz  not null default now()
);

create unique index if not exists exercise_name_index on exercises (category_id, name); -- for checking the exercise name is unique in the category


