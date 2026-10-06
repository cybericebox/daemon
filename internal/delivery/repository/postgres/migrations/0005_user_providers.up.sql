create table user_providers
(
    id               uuid primary key,
    user_id          uuid         not null references users (id) on delete cascade,
    provider         varchar(32)  not null,
    provider_user_id varchar(255) not null,
    created_at       timestamptz  not null default now()
);

create unique index user_provider_identity_uidx on user_providers (provider, provider_user_id);
create index user_provider_user_idx on user_providers (user_id);
