-- Task ids the author has checked correctly in this test deploy.
ALTER TABLE exercise_test_deployments ADD COLUMN solved jsonb NOT NULL DEFAULT '[]'::jsonb;
