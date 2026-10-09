ALTER TABLE event_configs DROP CONSTRAINT event_configs_lab_policy_check;
ALTER TABLE event_configs DROP COLUMN lab_policy;
ALTER TABLE lab_bindings DROP CONSTRAINT lab_bindings_shared_lab_fk;
ALTER TABLE lab_bindings DROP COLUMN lab_id;
DROP TABLE event_lab_objectives;
DROP TABLE event_team_labs;
