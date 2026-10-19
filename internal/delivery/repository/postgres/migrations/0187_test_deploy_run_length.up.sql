-- The run length the author chose for a test lab: its lease counts only while the lab runs, so the end is moved by it.
ALTER TABLE exercise_test_deployments ADD COLUMN run_seconds integer;
