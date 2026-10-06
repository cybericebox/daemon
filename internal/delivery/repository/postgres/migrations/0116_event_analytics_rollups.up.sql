-- Event analytics derived data (docs/EVENT-ANALYTICS.md §3 D5, §5). Both
-- tables are rebuilt from their sources by the analytics job, so every write
-- is idempotent.

-- event_vpn_sessions: VPN usage derived from the WireGuard handshakes in
-- event_lab_observations. A session is a run of handshakes of one client
-- (client_name, the lab client of a participant) without a long gap; bytes
-- are the counter growth inside it (max - min). user_id and client_name are
-- cleared when the account is deleted. Kept until the event's end plus 365
-- days, like the other analytics logs.
CREATE TABLE event_vpn_sessions
(
    id          uuid PRIMARY KEY,
    event_id    uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id     uuid        NOT NULL,
    user_id     uuid,
    client_name text        NOT NULL,
    started_at  timestamptz NOT NULL,
    ended_at    timestamptz NOT NULL CHECK (ended_at >= started_at),
    rx_min      bigint      NOT NULL CHECK (rx_min >= 0),
    rx_max      bigint      NOT NULL CHECK (rx_max >= rx_min),
    tx_min      bigint      NOT NULL CHECK (tx_min >= 0),
    tx_max      bigint      NOT NULL CHECK (tx_max >= tx_min)
);

CREATE INDEX event_vpn_sessions_event_idx ON event_vpn_sessions (event_id, ended_at);
CREATE INDEX event_vpn_sessions_user_idx ON event_vpn_sessions (user_id) WHERE user_id IS NOT NULL;

-- The VPN rollup reads each event's observations in arrival order.
CREATE INDEX event_lab_observations_event_received_idx
    ON event_lab_observations (event_id, received_at, id);

-- event_activity_buckets: 5-minute activity per team and task (attempts,
-- effectively correct attempts, solves, task opens). Per-team and per-task
-- series are sums over the other key. Aggregates only: no personal data, kept
-- with the event.
CREATE TABLE event_activity_buckets
(
    event_id     uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    bucket_at    timestamptz NOT NULL,
    team_id      uuid        NOT NULL,
    challenge_id uuid        NOT NULL,
    attempts     integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    correct      integer     NOT NULL DEFAULT 0 CHECK (correct >= 0),
    solves       integer     NOT NULL DEFAULT 0 CHECK (solves >= 0),
    opens        integer     NOT NULL DEFAULT 0 CHECK (opens >= 0),
    PRIMARY KEY (event_id, bucket_at, team_id, challenge_id)
);

-- event_analytics_rollups: per-event progress of the analytics job.
-- buckets_revision is the results revision the buckets were built at: a
-- finalized event is rebuilt only when its results change again (a late
-- decision). The VPN cursor is the last observation read (arrival order).
CREATE TABLE event_analytics_rollups
(
    event_id                 uuid PRIMARY KEY REFERENCES events (id) ON DELETE CASCADE,
    buckets_refreshed_at     timestamptz,
    buckets_revision         bigint      NOT NULL DEFAULT 0,
    buckets_finalized_at     timestamptz,
    vpn_cursor_received_at   timestamptz,
    vpn_cursor_id            uuid,
    vpn_finalized_at         timestamptz
);
