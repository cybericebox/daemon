-- Richer lab telemetry rollups and a journal of state transitions, rebuilt
-- from the raw observations (and their archive) by the analytics job.
--
-- Per device and minute: memory average (memory_sum / memory_samples), how
-- often the device was ready, and the last pod phase seen in the minute.
-- Rows written before this migration have memory_samples = 0 (unknown) until
-- the event is recomputed.
ALTER TABLE event_lab_usage_minutes
    ADD COLUMN memory_sum     bigint      NOT NULL DEFAULT 0 CHECK (memory_sum >= 0),
    ADD COLUMN memory_samples integer     NOT NULL DEFAULT 0 CHECK (memory_samples >= 0),
    ADD COLUMN ready_samples  integer     NOT NULL DEFAULT 0 CHECK (ready_samples >= 0),
    ADD COLUMN last_pod_phase text,
    ADD COLUMN last_phase_at  timestamptz;

-- Per lab and minute: how many samples saw the lab ready and its VPN ready.
CREATE TABLE event_lab_minutes
(
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id           uuid        NOT NULL,
    lab_group_name    text        NOT NULL,
    lab_name          text        NOT NULL,
    bucket_at         timestamptz NOT NULL,
    samples           integer     NOT NULL CHECK (samples > 0),
    ready_samples     integer     NOT NULL CHECK (ready_samples >= 0),
    vpn_ready_samples integer     NOT NULL CHECK (vpn_ready_samples >= 0),
    PRIMARY KEY (event_id, team_id, lab_group_name, lab_name, bucket_at)
);

CREATE INDEX event_lab_minutes_event_time_idx ON event_lab_minutes (event_id, bucket_at);

-- Per VPN client (a participant's WireGuard peer) and minute. rx_bytes and
-- tx_bytes are the traffic of the minute: the growth of the peer counters
-- between consecutive samples, a counter that went down counts as a reset (the
-- new value is the traffic since it). connected_samples are the samples whose
-- last handshake is within the online window. last_* carry the newest counter
-- values of the client so the next minute (or batch) continues from them.
CREATE TABLE event_vpn_client_minutes
(
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id           uuid        NOT NULL,
    lab_group_name    text        NOT NULL,
    client_name       text        NOT NULL,
    bucket_at         timestamptz NOT NULL,
    samples           integer     NOT NULL CHECK (samples > 0),
    connected_samples integer     NOT NULL CHECK (connected_samples >= 0),
    rx_bytes          bigint      NOT NULL CHECK (rx_bytes >= 0),
    tx_bytes          bigint      NOT NULL CHECK (tx_bytes >= 0),
    last_rx           bigint      NOT NULL CHECK (last_rx >= 0),
    last_tx           bigint      NOT NULL CHECK (last_tx >= 0),
    last_observed_at  timestamptz NOT NULL,
    PRIMARY KEY (event_id, team_id, lab_group_name, client_name, bucket_at)
);

CREATE INDEX event_vpn_client_minutes_event_time_idx ON event_vpn_client_minutes (event_id, bucket_at);

-- The journal of transitions seen in the telemetry. at is the agent's time
-- (the observation, or the moment the agent recorded for the fact), received_at
-- the platform's time of the observation, so the lag between what the agent saw
-- and what the platform knew is at -> received_at. detail carries the pod
-- phase, the VPN boot id, the policy name or the client's last handshake.
CREATE TABLE event_lab_transitions
(
    id             uuid PRIMARY KEY,
    event_id       uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id        uuid        NOT NULL,
    lab_group_name text        NOT NULL,
    lab_name       text        NOT NULL DEFAULT '',
    device_name    text,
    client_name    text,
    kind           text        NOT NULL CHECK (kind IN (
                       'lab_ready', 'lab_not_ready', 'device_ready', 'device_restart', 'pod_scheduled',
                       'pod_started', 'vpn_boot_changed', 'policy_applied', 'client_connected', 'client_disconnected')),
    at             timestamptz NOT NULL,
    received_at    timestamptz NOT NULL,
    detail         text
);

CREATE INDEX event_lab_transitions_event_time_idx ON event_lab_transitions (event_id, team_id, at);

-- The last known state of every lab, device, client, group and policy per
-- event: what the journal compares the next observation with. Small (one row
-- per entity), rebuilt together with the journal.
CREATE TABLE event_lab_journal_states
(
    event_id       uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id        uuid        NOT NULL,
    lab_group_name text        NOT NULL,
    kind           text        NOT NULL,
    lab_name       text        NOT NULL,
    name           text        NOT NULL,
    state          jsonb       NOT NULL,
    PRIMARY KEY (event_id, team_id, lab_group_name, kind, lab_name, name)
);

-- The journal reads each event's observations in arrival order behind its own
-- cursor; the rollup closes (lab_usage_finalized_at) once both cursors caught up.
ALTER TABLE event_analytics_rollups
    ADD COLUMN lab_journal_cursor_received_at timestamptz,
    ADD COLUMN lab_journal_cursor_id          uuid;
