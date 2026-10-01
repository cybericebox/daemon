-- The single catalog status of an exercise, derived from its version pointers:
-- archived > never published (draft_only / none) > published with a working
-- copy whose content differs (changed) > published. The content comparison is
-- the card's one (ExerciseDraftDiffersFromPublished: jsonb equality of the
-- variants plus the admin note) and runs only when both pointers are set.
-- p_draft_visible = false (a reader of the published version only) never
-- reports "changed": such a reader does not learn about the working copy.
CREATE FUNCTION exercise_status(p_draft_version_id uuid, p_published_version_id uuid, p_archived boolean,
                                p_draft_visible boolean)
    RETURNS text
    LANGUAGE sql
    STABLE
AS
$$
SELECT CASE
           WHEN p_archived THEN 'archived'
           WHEN p_published_version_id IS NULL THEN
               CASE WHEN p_draft_version_id IS NULL THEN 'none' ELSE 'draft_only' END
           WHEN p_draft_version_id IS NULL OR NOT p_draft_visible THEN 'published'
           WHEN COALESCE((SELECT d.variants <> p.variants OR d.admin_note <> p.admin_note
                          FROM exercise_versions d,
                               exercise_versions p
                          WHERE d.id = p_draft_version_id
                            AND p.id = p_published_version_id), false) THEN 'changed'
           ELSE 'published'
           END
$$;

