-- A user closes the row of a failed (or interrupted) import. A dismissal counts only until the job changes again
-- (a restart moves updated_at past it), so a rerun import shows up again.
CREATE TABLE exercise_import_dismissals (
    job_id       uuid PRIMARY KEY REFERENCES exercise_import_jobs (id) ON DELETE CASCADE,
    dismissed_at timestamptz NOT NULL
);
