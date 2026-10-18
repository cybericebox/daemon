ALTER TABLE exercise_import_jobs
    DROP COLUMN stage,
    DROP COLUMN images_done,
    DROP COLUMN images_total,
    DROP COLUMN tasks_done,
    DROP COLUMN tasks_total,
    DROP COLUMN bytes_pushed,
    DROP COLUMN bytes_total;
