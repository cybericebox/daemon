-- Authored metadata belongs to the same immutable snapshot as variants.
-- Older names/descriptions/tags were not recorded. Backfill the currently known
-- baseline for every historical row; this does not reconstruct lost history.
-- One migration statement batch is one transaction. Lock all protected
-- tables before taking the baseline so an old writer cannot slip between
-- the backfill and guard installation during RollingUpdate.
LOCK TABLE exercises, exercise_versions, exercise_files, file_references, exercise_event_access IN ACCESS EXCLUSIVE MODE;

ALTER TABLE exercise_versions
    ADD COLUMN name text,
    ADD COLUMN description text,
    ADD COLUMN tags text[];

UPDATE exercise_versions v
SET name = e.name, description = e.description, tags = e.tags
FROM exercises e
WHERE e.id = v.exercise_id;

ALTER TABLE exercise_versions
    ALTER COLUMN name SET NOT NULL,
    ALTER COLUMN description SET NOT NULL,
    ALTER COLUMN tags SET NOT NULL,
    ADD CONSTRAINT exercise_version_name_not_empty CHECK (length(btrim(name)) > 0);

-- New binary capability, not authentication. Main/job/migration connections
-- set it in their pgx startup parameters. Old pod mutations fail atomically
-- with a recognizable retryable error until the rollout reaches a new pod.
-- No implicit metadata defaults: even a capable insert must provide all
-- three authored fields. Empty description/tags remain legitimate values.
CREATE FUNCTION require_versioned_catalog_writer() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('cybericebox.catalog_metadata_writer', true) IS DISTINCT FROM '1' THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'catalog_metadata_writer capability required',
            DETAIL = 'This backend predates versioned catalog metadata; no catalog mutation was applied.',
            HINT = 'Retry the catalog operation after the backend update completes.';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER exercise_writer_insert_delete BEFORE INSERT OR DELETE ON exercises
FOR EACH ROW EXECUTE FUNCTION require_versioned_catalog_writer();
CREATE TRIGGER exercise_writer_update BEFORE UPDATE OF name, description, tags, archived_at, access_level,
    scope, owner_event_id, origin_event_id, forked_from_exercise_id, forked_from_version_id,
    draft_version_id, published_version_id ON exercises
FOR EACH ROW EXECUTE FUNCTION require_versioned_catalog_writer();
CREATE TRIGGER exercise_version_writer BEFORE INSERT OR UPDATE OR DELETE ON exercise_versions
FOR EACH ROW EXECUTE FUNCTION require_versioned_catalog_writer();
CREATE TRIGGER exercise_file_writer BEFORE INSERT OR UPDATE OR DELETE ON exercise_files
FOR EACH ROW EXECUTE FUNCTION require_versioned_catalog_writer();
CREATE TRIGGER exercise_access_writer BEFORE INSERT OR UPDATE OR DELETE ON exercise_event_access
FOR EACH ROW EXECUTE FUNCTION require_versioned_catalog_writer();
CREATE TRIGGER exercise_reference_insert BEFORE INSERT ON file_references
FOR EACH ROW WHEN (NEW.ref_type = 'exercise_version') EXECUTE FUNCTION require_versioned_catalog_writer();
CREATE TRIGGER exercise_reference_update BEFORE UPDATE ON file_references
FOR EACH ROW WHEN (OLD.ref_type = 'exercise_version' OR NEW.ref_type = 'exercise_version') EXECUTE FUNCTION require_versioned_catalog_writer();
CREATE TRIGGER exercise_reference_delete BEFORE DELETE ON file_references
FOR EACH ROW WHEN (OLD.ref_type = 'exercise_version') EXECUTE FUNCTION require_versioned_catalog_writer();

CREATE OR REPLACE FUNCTION exercise_status(p_draft_version_id uuid, p_published_version_id uuid, p_archived boolean,
                                          p_draft_visible boolean)
    RETURNS text LANGUAGE sql STABLE AS $$
SELECT CASE
    WHEN p_archived THEN 'archived'
    WHEN p_published_version_id IS NULL THEN CASE WHEN p_draft_version_id IS NULL THEN 'none' ELSE 'draft_only' END
    WHEN p_draft_version_id IS NULL OR NOT p_draft_visible THEN 'published'
    WHEN COALESCE((SELECT d.variants <> p.variants OR d.admin_note <> p.admin_note
                      OR d.name <> p.name OR d.description <> p.description OR d.tags <> p.tags
                   FROM exercise_versions d, exercise_versions p
                   WHERE d.id = p_draft_version_id AND p.id = p_published_version_id), false) THEN 'changed'
    ELSE 'published' END
$$;
