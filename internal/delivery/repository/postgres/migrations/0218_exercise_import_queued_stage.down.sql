UPDATE exercise_import_jobs SET stage = 'unpacking' WHERE stage = 'queued';
ALTER TABLE exercise_import_jobs DROP CONSTRAINT IF EXISTS exercise_import_jobs_stage_check;
ALTER TABLE exercise_import_jobs
    ADD CONSTRAINT exercise_import_jobs_stage_check CHECK (stage IN ('unpacking', 'images', 'tasks', 'done', 'failed'));
