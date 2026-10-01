-- Exercise catalog: identity aggregate + versioned JSONB content snapshots.
-- FK cycle exercises<->exercise_versions is resolved by creating exercises
-- first and adding the pointer columns after the versions table exists.
create table if not exists exercises
(
    id          uuid primary key,
    name        text        not null unique,
    description text        not null default '',
    tags        text[]      not null default '{}',
    created_at  timestamptz not null,
    created_by  uuid references users (id) on delete set null,
    updated_at  timestamptz not null,
    updated_by  uuid references users (id) on delete set null
);

create table if not exists exercise_versions
(
    id           uuid primary key,
    exercise_id  uuid        not null references exercises (id) on delete cascade,
    status       text        not null,
    admin_note   text        not null default '',
    regen_flags  boolean     not null default false,
    -- content snapshot: [{index, tasks[], topology}] as one logical document
    variants     jsonb       not null default '[]',
    created_at   timestamptz not null,
    created_by   uuid references users (id) on delete set null,
    published_at timestamptz
);

create index if not exists exercise_versions_exercise_idx
    on exercise_versions (exercise_id, created_at desc);
-- family invariants: at most one draft / one published per exercise
create unique index if not exists uq_exercise_version_draft
    on exercise_versions (exercise_id) where status = 'draft';
create unique index if not exists uq_exercise_version_published
    on exercise_versions (exercise_id) where status = 'published';

alter table exercises
    add column if not exists draft_version_id     uuid references exercise_versions (id) on delete set null,
    add column if not exists published_version_id uuid references exercise_versions (id) on delete set null;
