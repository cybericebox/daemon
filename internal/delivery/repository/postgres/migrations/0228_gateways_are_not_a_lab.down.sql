DROP FUNCTION task_offers_vpn_access(jsonb, jsonb, uuid);

CREATE OR REPLACE FUNCTION exercise_variants_have_infrastructure(p_variants jsonb)
    RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT COALESCE(jsonb_path_exists(p_variants, '$[*].topology.devices[*] ? (@.type() == "object")'), false)
$$;
