-- A queued import (not picked by a worker yet) is told apart from a started one: only a started import without a
-- heartbeat is interrupted.
ALTER TABLE exercise_import_jobs DROP CONSTRAINT IF EXISTS exercise_import_jobs_stage_check;
ALTER TABLE exercise_import_jobs
    ADD CONSTRAINT exercise_import_jobs_stage_check CHECK (stage IN ('queued', 'unpacking', 'images', 'tasks', 'done', 'failed'));
