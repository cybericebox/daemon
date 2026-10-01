CREATE OR REPLACE FUNCTION exercise_has_infrastructure(p_exercise_id uuid)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT COALESCE((SELECT jsonb_path_exists(version.variants, '$[*].topology.devices[*]')
                 FROM exercises e
                 JOIN exercise_versions version ON version.id = COALESCE(e.published_version_id, e.draft_version_id)
                 WHERE e.id = p_exercise_id), false)
$$;

DROP FUNCTION exercise_variants_have_infrastructure(jsonb);
