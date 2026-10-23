DROP TRIGGER exercise_reference_delete ON file_references;
DROP TRIGGER exercise_reference_update ON file_references;
DROP TRIGGER exercise_reference_insert ON file_references;
DROP TRIGGER exercise_access_writer ON exercise_event_access;
DROP TRIGGER exercise_file_writer ON exercise_files;
DROP TRIGGER exercise_version_writer ON exercise_versions;
DROP TRIGGER exercise_writer_update ON exercises;
DROP TRIGGER exercise_writer_insert_delete ON exercises;
DROP FUNCTION require_versioned_catalog_writer();

CREATE OR REPLACE FUNCTION exercise_status(p_draft_version_id uuid, p_published_version_id uuid, p_archived boolean,
                                          p_draft_visible boolean)
    RETURNS text LANGUAGE sql STABLE AS $$
SELECT CASE
    WHEN p_archived THEN 'archived'
    WHEN p_published_version_id IS NULL THEN CASE WHEN p_draft_version_id IS NULL THEN 'none' ELSE 'draft_only' END
    WHEN p_draft_version_id IS NULL OR NOT p_draft_visible THEN 'published'
    WHEN COALESCE((SELECT d.variants <> p.variants OR d.admin_note <> p.admin_note
                   FROM exercise_versions d, exercise_versions p
                   WHERE d.id = p_draft_version_id AND p.id = p_published_version_id), false) THEN 'changed'
    ELSE 'published' END
$$;

ALTER TABLE exercise_versions DROP COLUMN tags, DROP COLUMN description, DROP COLUMN name;
