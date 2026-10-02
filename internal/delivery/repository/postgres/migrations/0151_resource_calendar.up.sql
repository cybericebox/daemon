-- Resource calendar: reservations of lab resources over 15-minute slots. They live only in the backend (the
-- agent keeps just the tenant quota). kind: event (set by a platform admin) or test_booking (a catalog author's
-- window for a test laboratory). Sizes are CPU millicores and memory bytes; the window is slot-aligned
-- [starts_at, ends_at). placement is the split over agents, [{agent_id, units}]: a team (unit) is whole on one
-- agent; unplaced counts the teams no agent could take.
CREATE TABLE resource_reservations
(
    id                            uuid PRIMARY KEY,
    kind                          text        NOT NULL CHECK (kind IN ('event', 'test_booking')),
    event_id                      uuid        REFERENCES events (id) ON DELETE CASCADE,
    owner_id                      uuid        REFERENCES users (id) ON DELETE CASCADE,
    starts_at                     timestamptz NOT NULL,
    ends_at                       timestamptz NOT NULL,
    teams                         integer     NOT NULL CHECK (teams >= 1),
    per_team_cpu_millicores       bigint      NOT NULL CHECK (per_team_cpu_millicores >= 0),
    per_team_memory_bytes         bigint      NOT NULL CHECK (per_team_memory_bytes >= 0),
    largest_device_cpu_millicores bigint      NOT NULL DEFAULT 0,
    largest_device_memory_bytes   bigint      NOT NULL DEFAULT 0,
    buffer_percent                integer     NOT NULL DEFAULT 15 CHECK (buffer_percent BETWEEN 0 AND 200),
    dynamic_cpu_millicores        bigint      NOT NULL DEFAULT 0 CHECK (dynamic_cpu_millicores >= 0),
    dynamic_memory_bytes          bigint      NOT NULL DEFAULT 0 CHECK (dynamic_memory_bytes >= 0),
    tail_gap_seconds              integer     NOT NULL DEFAULT 3600 CHECK (tail_gap_seconds >= 0),
    size_cpu_millicores           bigint      NOT NULL CHECK (size_cpu_millicores >= 0),
    size_memory_bytes             bigint      NOT NULL CHECK (size_memory_bytes >= 0),
    placement                     jsonb       NOT NULL DEFAULT '[]',
    unplaced                      integer     NOT NULL DEFAULT 0 CHECK (unplaced >= 0),
    created_by                    uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at                    timestamptz NOT NULL,
    updated_at                    timestamptz NOT NULL,
    canceled_at                   timestamptz,
    CHECK (ends_at > starts_at),
    CHECK ((kind = 'event' AND event_id IS NOT NULL) OR (kind = 'test_booking' AND owner_id IS NOT NULL))
);
CREATE UNIQUE INDEX resource_reservations_event_uq ON resource_reservations (event_id) WHERE kind = 'event' AND canceled_at IS NULL;
CREATE INDEX resource_reservations_window_idx ON resource_reservations (starts_at, ends_at) WHERE canceled_at IS NULL;
CREATE INDEX resource_reservations_owner_idx ON resource_reservations (owner_id, starts_at) WHERE kind = 'test_booking' AND canceled_at IS NULL;

-- An organizer's request to change an event's reservation (size, window, the estimate for future dynamic
-- tasks) with a reason; a platform admin decides. status: 0 pending, 1 approved, 2 rejected. A null column is
-- left as it is.
CREATE TABLE resource_change_requests
(
    id                   uuid PRIMARY KEY,
    reservation_id       uuid        NOT NULL REFERENCES resource_reservations (id) ON DELETE CASCADE,
    event_id             uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    requested_by         uuid        REFERENCES users (id) ON DELETE SET NULL,
    requested_at         timestamptz NOT NULL,
    size_cpu_millicores  bigint,
    size_memory_bytes    bigint,
    dynamic_cpu_millicores bigint,
    dynamic_memory_bytes bigint,
    window_start         timestamptz,
    window_end           timestamptz,
    reason               text        NOT NULL,
    status               smallint    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    decided_by           uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at           timestamptz,
    decision_note        text        NOT NULL DEFAULT ''
);
CREATE INDEX resource_change_requests_status_idx ON resource_change_requests (status, requested_at DESC);
CREATE INDEX resource_change_requests_event_idx ON resource_change_requests (event_id, requested_at DESC);

-- Readiness alarms: a reservation that cannot be served as promised (not placed, its agent gone or shrunk, the
-- connected capacity below it). One open alarm per cause; resolved when the cause goes away. stage is how far
-- the escalation went (0 first sight, 1 day before the deploy lead, 2 two hours before, 3 at the lead).
CREATE TABLE resource_alarms
(
    id                    uuid PRIMARY KEY,
    kind                  text        NOT NULL CHECK (kind IN ('not_placed', 'agent_lost', 'agent_shrunk', 'not_connected')),
    reservation_id        uuid        NOT NULL REFERENCES resource_reservations (id) ON DELETE CASCADE,
    event_id              uuid        REFERENCES events (id) ON DELETE CASCADE,
    agent_id              uuid,
    units                 integer     NOT NULL DEFAULT 0,
    stage                 smallint    NOT NULL DEFAULT 0,
    shortage_cpu_millicores bigint    NOT NULL DEFAULT 0,
    shortage_memory_bytes bigint      NOT NULL DEFAULT 0,
    raised_at             timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL,
    resolved_at           timestamptz,
    acked_by              uuid        REFERENCES users (id) ON DELETE SET NULL,
    acked_at              timestamptz
);
CREATE UNIQUE INDEX resource_alarms_open_uq ON resource_alarms (kind, reservation_id, COALESCE(agent_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE resolved_at IS NULL;
CREATE INDEX resource_alarms_open_idx ON resource_alarms (raised_at DESC) WHERE resolved_at IS NULL;

-- Calendar settings (one row): the guaranteed minimum pool for test laboratories.
CREATE TABLE resource_calendar_settings
(
    id                       boolean PRIMARY KEY DEFAULT true CHECK (id),
    test_pool_cpu_millicores bigint      NOT NULL DEFAULT 0 CHECK (test_pool_cpu_millicores >= 0),
    test_pool_memory_bytes   bigint      NOT NULL DEFAULT 0 CHECK (test_pool_memory_bytes >= 0),
    updated_at               timestamptz NOT NULL DEFAULT now()
);
INSERT INTO resource_calendar_settings (id) VALUES (true);

-- What the running test laboratories hold: the room they were admitted with, until their lease ends.
CREATE TABLE resource_test_lab_holds
(
    id                    uuid PRIMARY KEY,
    owner_id              uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    via                   text        NOT NULL CHECK (via IN ('pool', 'free', 'booking')),
    reservation_id        uuid        REFERENCES resource_reservations (id) ON DELETE SET NULL,
    cpu_millicores        bigint      NOT NULL CHECK (cpu_millicores >= 0),
    memory_bytes          bigint      NOT NULL CHECK (memory_bytes >= 0),
    starts_at             timestamptz NOT NULL,
    expires_at            timestamptz NOT NULL
);
CREATE INDEX resource_test_lab_holds_expires_idx ON resource_test_lab_holds (expires_at);
