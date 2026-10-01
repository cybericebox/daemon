-- Media subsystem: content-addressed blobs (dedup by sha256) + logical files
-- + refcounted ownership references.
create table if not exists file_blobs
(
    content_hash text primary key, -- sha256 hex; S3 key = media/blobs/<hash>
    size_bytes   bigint      not null default 0,
    ref_count    integer     not null default 0,
    created_at   timestamptz not null,
    -- Bumped every time CreateFile references this blob (new upload or
    -- content-addressed dedup hit). GC's orphan listing requires a blob to
    -- have gone untouched for the grace window, not just ref_count <= 0:
    -- without it, a dedup upload racing a running GC pass (Stat sees the
    -- blob exists, skips the copy, then CreateFile bumps ref_count) can lose
    -- to GC's unconditional storage.Remove for a hash it listed as orphan
    -- just before the race — the row survives (DeleteBlob's ref_count <= 0
    -- guard correctly refuses it) but the S3 object is already gone.
    touched_at   timestamptz not null default now()
);

create table if not exists files
(
    id           uuid primary key,
    name         text        not null,
    content_type text        not null default '',
    size_bytes   bigint      not null default 0,
    content_hash text        not null references file_blobs (content_hash),
    created_at   timestamptz not null,
    created_by   uuid references users (id) on delete set null
);

create index if not exists files_hash_idx on files (content_hash);
create index if not exists files_created_idx on files (created_at);

create table if not exists file_references
(
    ref_type text not null,
    ref_id   uuid not null,
    file_id  uuid not null references files (id) on delete cascade,
    primary key (ref_type, ref_id, file_id)
);

create index if not exists file_references_file_idx on file_references (file_id);
