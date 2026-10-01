-- Valid only while no author has two test deploys (a group is shared by them).
ALTER TABLE exercise_test_deployments DROP COLUMN lab_name;
DROP INDEX exercise_test_deployments_group_idx;
ALTER TABLE exercise_test_deployments ADD CONSTRAINT exercise_test_deployments_group_name_key UNIQUE (group_name);
