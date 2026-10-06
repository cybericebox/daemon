CREATE TABLE infrastructure_agents
(
    id         uuid        PRIMARY KEY,
    key        text        NOT NULL UNIQUE,
    name       text        NOT NULL,
    configured boolean     NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
