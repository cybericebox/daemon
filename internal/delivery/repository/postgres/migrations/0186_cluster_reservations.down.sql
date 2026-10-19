DELETE FROM resource_alarms WHERE kind IN ('cluster_sync_failed', 'cluster_shrink_held');
ALTER TABLE resource_alarms DROP CONSTRAINT resource_alarms_kind_check;
ALTER TABLE resource_alarms ADD CONSTRAINT resource_alarms_kind_check
    CHECK (kind IN ('not_placed', 'agent_lost', 'agent_shrunk', 'not_connected'));
ALTER TABLE exercise_test_deployments DROP COLUMN IF EXISTS ending_at;
DROP TABLE IF EXISTS cluster_reservation_units;
DROP TABLE IF EXISTS cluster_reservations;
