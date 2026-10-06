-- Per-user WireGuard VPN configs, kept OUT of the participants table: a config is
-- issued to any user (participant, moderator, admin testing) and is stored
-- encrypted, so it does not belong on a role-specific row.
--
-- A user can hold several configs at once (different events, plus a test config),
-- so rows are discriminated by a scope: 'event' with scope_ref = the event id, or
-- 'test' with a null ref (a moderator/admin standing a variant up outside any
-- event). NULLS NOT DISTINCT makes the null-ref case unique per (user, scope),
-- so a user has at most one test config.
create table if not exists user_vpn_configs
(
    id         uuid        primary key,
    user_id    uuid        not null references users (id) on delete cascade,
    scope      text        not null,
    scope_ref  uuid,
    config     text        not null,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    unique nulls not distinct (user_id, scope, scope_ref)
);

create index if not exists user_vpn_configs_user_index
    on user_vpn_configs (user_id);
