-- Logical retained-snapshot reservations. Old rows are unconfigured (zero), not physical measurements.
ALTER TABLE resource_reservations
 ADD COLUMN per_team_snapshot_quota_bytes bigint NOT NULL DEFAULT 0 CHECK(per_team_snapshot_quota_bytes>=0),
 ADD COLUMN dynamic_snapshot_quota_bytes bigint NOT NULL DEFAULT 0 CHECK(dynamic_snapshot_quota_bytes>=0),
 ADD COLUMN size_snapshot_quota_bytes bigint NOT NULL DEFAULT 0 CHECK(size_snapshot_quota_bytes>=0);
ALTER TABLE resource_change_requests
 ADD COLUMN size_snapshot_quota_bytes bigint CHECK(size_snapshot_quota_bytes>=0),
 ADD COLUMN dynamic_snapshot_quota_bytes bigint CHECK(dynamic_snapshot_quota_bytes>=0);
ALTER TABLE resource_calendar_settings ADD COLUMN test_pool_snapshot_quota_bytes bigint NOT NULL DEFAULT 0 CHECK(test_pool_snapshot_quota_bytes>=0);
ALTER TABLE resource_test_lab_holds ADD COLUMN snapshot_quota_bytes bigint NOT NULL DEFAULT 0 CHECK(snapshot_quota_bytes>=0);
-- One immutable pod-size envelope per participation unit. Child release cannot release this overhead.
CREATE TABLE event_team_group_allocations (
 event_team_id uuid PRIMARY KEY,
 event_id uuid NOT NULL,
 lab_group_name text NOT NULL,
 vpn_cpu_millicores bigint NOT NULL CHECK(vpn_cpu_millicores>=0),
 vpn_memory_bytes bigint NOT NULL CHECK(vpn_memory_bytes>=0),
 gateway_cpu_millicores bigint NOT NULL CHECK(gateway_cpu_millicores>=0),
 gateway_memory_bytes bigint NOT NULL CHECK(gateway_memory_bytes>=0),
 plan jsonb NOT NULL,
 created_at timestamptz NOT NULL
);
CREATE INDEX event_team_group_allocations_event ON event_team_group_allocations(event_id);
