-- Fails if shared lab names exist; remove the duplicates first.
DROP INDEX lab_bindings_lab_idx;
ALTER TABLE lab_bindings ADD CONSTRAINT lab_bindings_lab_group_name_lab_name_key UNIQUE (lab_group_name, lab_name);
