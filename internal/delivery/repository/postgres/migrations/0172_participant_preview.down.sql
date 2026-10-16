DELETE FROM resource_test_lab_holds WHERE via IN ('event_preview','event_preview_group');
ALTER TABLE resource_test_lab_holds DROP CONSTRAINT resource_test_lab_holds_via_check;
ALTER TABLE resource_test_lab_holds ADD CONSTRAINT resource_test_lab_holds_via_check CHECK (via IN ('pool','free','booking'));
ALTER TABLE resource_test_lab_holds DROP COLUMN group_name;
ALTER TABLE resource_test_lab_holds DROP COLUMN event_id;
ALTER TABLE exercise_test_deployments DROP COLUMN event_id;
DROP TABLE event_preview_runs;
DROP TABLE event_preview_settings;
