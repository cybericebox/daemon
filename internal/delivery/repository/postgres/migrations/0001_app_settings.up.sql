CREATE TABLE app_settings
(
    id          uuid PRIMARY KEY,
    key         text        NOT NULL UNIQUE,
    value       jsonb       NOT NULL,
    read_access smallint    NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
