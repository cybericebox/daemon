DROP TRIGGER IF EXISTS exercise_version_revision ON exercise_versions;
DROP FUNCTION IF EXISTS exercise_version_revision();
ALTER TABLE exercise_versions
    DROP COLUMN IF EXISTS updated_by,
    DROP COLUMN IF EXISTS updated_at;
