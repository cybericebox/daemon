-- One Lab per team and event exercise: every challenge row of the set shares the lab name.
ALTER TABLE lab_bindings DROP CONSTRAINT lab_bindings_lab_group_name_lab_name_key;
CREATE INDEX lab_bindings_lab_idx ON lab_bindings (lab_group_name, lab_name);
