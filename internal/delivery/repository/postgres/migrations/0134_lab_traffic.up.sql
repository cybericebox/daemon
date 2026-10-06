-- Per-user lab access accounting (docs/LAB-TRAFFIC-ACCOUNTING.md). Counts and
-- times only: no payloads, no user network addresses, no time series. The unit
-- is a participant's VPN config or proxy token, not a person.

-- One final row per event x team x user x lab (task) x access type. The
-- collector keeps CUMULATIVE totals (persisted in the cluster, so a restart
-- resumes from them) and every report sends the full current values; the
-- ingest overwrites: totals take the greater of stored and reported,
-- first_seen_at is min, last_seen_at is max, first_responded_at is the earliest
-- non-null. attempts_count is new connections (vpn) or requests (proxy).
-- first_responded_at NULL means the lab never answered (the «responded» flag is
-- NOT NULL).
CREATE TABLE event_lab_touches
(
    id                 uuid PRIMARY KEY,
    event_id           uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id            uuid        NOT NULL,
    user_id            uuid,
    event_challenge_id uuid        NOT NULL,
    surface            text        NOT NULL CHECK (surface IN ('vpn', 'proxy')),
    attempts_count     bigint      NOT NULL DEFAULT 0,
    first_seen_at      timestamptz NOT NULL,
    last_seen_at       timestamptz NOT NULL,
    first_responded_at timestamptz,
    packets_out        bigint      NOT NULL DEFAULT 0,
    packets_in         bigint      NOT NULL DEFAULT 0,
    bytes_out          bigint      NOT NULL DEFAULT 0,
    bytes_in           bigint      NOT NULL DEFAULT 0
);

-- Rows of deleted accounts keep user_id NULL and are outside the key.
CREATE UNIQUE INDEX event_lab_touches_key_idx
    ON event_lab_touches (event_id, team_id, user_id, event_challenge_id, surface)
    WHERE user_id IS NOT NULL;
CREATE INDEX event_lab_touches_lookup_idx
    ON event_lab_touches (event_id, event_challenge_id, team_id, first_seen_at);
CREATE INDEX event_lab_touches_user_idx ON event_lab_touches (user_id) WHERE user_id IS NOT NULL;

-- The spans a collector really observed, so «did not touch» can be told from
-- «no data». Extended by every report, also when nothing happened.
CREATE TABLE lab_traffic_coverage
(
    id           uuid PRIMARY KEY,
    event_id     uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id      uuid        NOT NULL,
    surface      text        NOT NULL CHECK (surface IN ('vpn', 'proxy')),
    source       text        NOT NULL,
    boot_id      text        NOT NULL,
    covered_from timestamptz NOT NULL,
    covered_to   timestamptz NOT NULL,
    partial      boolean     NOT NULL DEFAULT false
);

CREATE INDEX lab_traffic_coverage_idx ON lab_traffic_coverage (event_id, team_id, surface, covered_to);
