create table if not exists event_configs
(
    event_id                 uuid primary key references events (id) on delete cascade,
    participation            smallint,               -- null until set-once; then immutable
    registration             smallint    not null,
    registration_after_start boolean     not null,
    scoreboard_visibility    smallint    not null,
    participants_visibility  smallint    not null,
    preview_description      text        not null default '',
    preview_picture          text        not null default '',
    created_at               timestamptz not null,
    updated_at               timestamptz,
    updated_by               uuid references users (id) on delete set null
);

create table if not exists event_participants
(
    event_id   uuid        not null references events (id) on delete cascade,
    user_id    uuid        not null references users (id) on delete cascade,
    status     smallint    not null,
    created_at timestamptz not null,
    decided_at timestamptz,
    decided_by uuid references users (id) on delete set null,
    primary key (event_id, user_id)
);

-- moderation list is filtered by (event, status)
create index if not exists event_participants_event_status_index
    on event_participants (event_id, status);
