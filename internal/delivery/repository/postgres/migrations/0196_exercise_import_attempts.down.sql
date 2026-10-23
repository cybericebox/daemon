DROP TRIGGER exercise_import_writer ON exercise_import_jobs;
ALTER TABLE exercise_import_jobs
    DROP COLUMN results,
    DROP COLUMN plan_hash,
    DROP COLUMN options,
    DROP COLUMN generation;
