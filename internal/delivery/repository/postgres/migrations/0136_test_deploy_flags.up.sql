-- Test values injected into the linked devices, kept server-side so the author's found flags can be checked.
ALTER TABLE exercise_test_deployments ADD COLUMN flags jsonb NOT NULL DEFAULT '[]'::jsonb;
