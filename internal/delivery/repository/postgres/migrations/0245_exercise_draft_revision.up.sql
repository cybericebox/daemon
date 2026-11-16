-- The revision of a working copy: when and by whom a version row was last written. The database assigns it on every
-- change of the row (never the application clock), so a client that echoes the value it read can be checked atomically
-- (UPDATE ... WHERE updated_at = $expected). A new column with a default keeps the writers of the previous release working.
ALTER TABLE exercise_versions
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN updated_by uuid REFERENCES users (id) ON DELETE SET NULL;

UPDATE exercise_versions
SET updated_at = GREATEST(created_at, COALESCE(published_at, created_at)),
    updated_by = created_by;

-- Every real change of a row moves the revision strictly forward (microsecond timestamptz, never equal to the one before),
-- whichever statement or release wrote it. A write that changes nothing keeps it: only the columns an editor can see are compared
-- (not the generated avg_difficulty, which a BEFORE trigger cannot see computed, nor ids and creation data). A transaction that only re-encrypts stored
-- secrets (key rotation) says so with cybericebox.keep_revision = 1 and leaves the revision alone: the editor sees the same
-- content, so an editor in the middle of a session is not refused for it.
CREATE FUNCTION exercise_version_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF coalesce(current_setting('cybericebox.keep_revision', true), '') = '1' THEN
        NEW.updated_at := OLD.updated_at;
    ELSIF (NEW.exercise_id, NEW.status, NEW.admin_note, NEW.variants, NEW.label, NEW.name, NEW.description, NEW.tags,
           NEW.published_at, NEW.updated_by)
          IS DISTINCT FROM
          (OLD.exercise_id, OLD.status, OLD.admin_note, OLD.variants, OLD.label, OLD.name, OLD.description, OLD.tags,
           OLD.published_at, OLD.updated_by) THEN
        NEW.updated_at := GREATEST(clock_timestamp(), OLD.updated_at + interval '1 microsecond');
    ELSE
        NEW.updated_at := OLD.updated_at;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER exercise_version_revision BEFORE UPDATE ON exercise_versions
FOR EACH ROW EXECUTE FUNCTION exercise_version_revision();
