create table if not exists event_challenge_categories
(
    id          uuid primary key,
    event_id    uuid         not null references events (id) on delete restrict,

    name        varchar(255) not null,
    order_index integer      not null,

    updated_at  timestamptz,
    updated_by  uuid         references users (id) on delete set null,

    created_at  timestamptz  not null default now()
);

create unique index if not exists event_challenge_category_index on event_challenge_categories (event_id, name); -- for checking the category name is unique in the event
