-- How far a running background import has come: the stage and the counts the user sees. Written narrowly and
-- throttled (UpdateExerciseImportJobProgress) while the job runs.
ALTER TABLE exercise_import_jobs
    ADD COLUMN stage        text   NOT NULL DEFAULT 'unpacking' CHECK (stage IN ('unpacking', 'images', 'tasks', 'done', 'failed')),
    ADD COLUMN images_done  int    NOT NULL DEFAULT 0,
    ADD COLUMN images_total int    NOT NULL DEFAULT 0,
    ADD COLUMN tasks_done   int    NOT NULL DEFAULT 0,
    ADD COLUMN tasks_total  int    NOT NULL DEFAULT 0,
    ADD COLUMN bytes_pushed bigint NOT NULL DEFAULT 0,
    ADD COLUMN bytes_total  bigint NOT NULL DEFAULT 0;
