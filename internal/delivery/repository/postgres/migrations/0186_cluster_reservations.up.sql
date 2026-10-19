-- Cluster reservations: the backend's reservations as the agents hold them. A part is one cluster reservation on
-- one agent; its id is what the agent knows and stays the same when the part is extended. Parts come from a
-- calendar reservation (split by its placement shares), a test run (one lab, from its start to the end of its
-- lease) or a test group (an author's shared group on one agent). desired_rev counts the changes to send,
-- synced_rev is the last one the agent confirmed (sync_error / sync_reason / sync_failed_since say why it is not,
-- shrink_held that a shrink was held back while labs run in the part); forced marks a desired change an admin forced
-- past that guard. The observed columns are what the agent last reported.
CREATE TABLE cluster_reservations
(
    id                      uuid PRIMARY KEY,
    agent_id                uuid        NOT NULL,
    source                  text        NOT NULL CHECK (source IN ('calendar', 'test_run', 'test_group')),
    calendar_reservation_id uuid        REFERENCES resource_reservations (id) ON DELETE SET NULL,
    test_deploy_id          uuid,
    owner_id                uuid,
    starts_at               timestamptz NOT NULL,
    ends_at                 timestamptz NOT NULL,
    cpu_millicores          bigint      NOT NULL CHECK (cpu_millicores > 0),
    memory_bytes            bigint      NOT NULL CHECK (memory_bytes > 0),
    desired_rev             bigint      NOT NULL DEFAULT 1,
    synced_rev              bigint      NOT NULL DEFAULT 0,
    synced_at               timestamptz,
    sync_error              text        NOT NULL DEFAULT '',
    sync_reason             text        NOT NULL DEFAULT '',
    sync_failed_since       timestamptz,
    shrink_held             boolean     NOT NULL DEFAULT false,
    forced                  boolean     NOT NULL DEFAULT false,
    phase                   text        NOT NULL DEFAULT '',
    notice_at               timestamptz,
    ends_notice_at          timestamptz,
    ending_reason           text        NOT NULL DEFAULT '',
    used_cpu_millicores     bigint      NOT NULL DEFAULT 0,
    used_memory_bytes       bigint      NOT NULL DEFAULT 0,
    group_count             integer     NOT NULL DEFAULT 0,
    exceeded                boolean     NOT NULL DEFAULT false,
    observed_at             timestamptz,
    retire_at               timestamptz,
    created_at              timestamptz NOT NULL,
    updated_at              timestamptz NOT NULL,
    CHECK (ends_at > starts_at)
);
CREATE UNIQUE INDEX cluster_reservations_calendar_agent_uq ON cluster_reservations (calendar_reservation_id, agent_id)
    WHERE source = 'calendar' AND retire_at IS NULL;
CREATE UNIQUE INDEX cluster_reservations_run_uq ON cluster_reservations (test_deploy_id)
    WHERE source = 'test_run' AND retire_at IS NULL;
CREATE UNIQUE INDEX cluster_reservations_group_uq ON cluster_reservations (owner_id, agent_id)
    WHERE source = 'test_group' AND retire_at IS NULL;
CREATE INDEX cluster_reservations_unsynced_idx ON cluster_reservations (agent_id)
    WHERE synced_rev < desired_rev OR retire_at IS NOT NULL;
CREATE INDEX cluster_reservations_agent_idx ON cluster_reservations (agent_id);

-- The agent of each unit of a calendar reservation (a team, a preview owner, the booking): a unit stays on its
-- agent while its group exists.
CREATE TABLE cluster_reservation_units
(
    calendar_reservation_id uuid        NOT NULL REFERENCES resource_reservations (id) ON DELETE CASCADE,
    unit_key                text        NOT NULL,
    agent_id                uuid        NOT NULL,
    assigned_at             timestamptz NOT NULL,
    PRIMARY KEY (calendar_reservation_id, unit_key)
);

-- Two new readiness alarms: a part the agent did not take for a while, and a shrink held back because labs
-- still run in the part.
ALTER TABLE resource_alarms DROP CONSTRAINT resource_alarms_kind_check;
ALTER TABLE resource_alarms ADD CONSTRAINT resource_alarms_kind_check
    CHECK (kind IN ('not_placed', 'agent_lost', 'agent_shrunk', 'not_connected', 'cluster_sync_failed', 'cluster_shrink_held'));
