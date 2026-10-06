create table if not exists events
(
    id             uuid primary key,
    tag            varchar(64)  not null,
    name           varchar(255) not null default '',
    available_from timestamptz  not null,
    archive_at     timestamptz  not null,
    updated_at     timestamptz,
    updated_by     uuid references users (id) on delete set null,
    created_at     timestamptz  not null default now(),
    created_by     uuid references users (id) on delete set null
);

-- Tag is the event subdomain; allow reuse after archival (like the legacy
-- (tag, withdraw_time) composite) by keying uniqueness on (tag, archive_at).
create unique index if not exists event_tag_index on events (tag, archive_at);
