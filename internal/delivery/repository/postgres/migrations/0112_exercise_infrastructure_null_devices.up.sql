-- A topology saved without devices stores "devices": null, and the lax
-- jsonpath '$[*].topology.devices[*]' wraps that null into one item, so an
-- exercise with no lab counted as infrastructure. Only a device object counts.
CREATE FUNCTION exercise_variants_have_infrastructure(p_variants jsonb)
    RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT COALESCE(jsonb_path_exists(p_variants, '$[*].topology.devices[*] ? (@.type() == "object")'), false)
$$;

CREATE OR REPLACE FUNCTION exercise_has_infrastructure(p_exercise_id uuid)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT COALESCE((SELECT exercise_variants_have_infrastructure(version.variants)
                 FROM exercises e
                 JOIN exercise_versions version ON version.id = COALESCE(e.published_version_id, e.draft_version_id)
                 WHERE e.id = p_exercise_id), false)
$$;
