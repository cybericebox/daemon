-- Back to the type "container". A node of another profile (microvm, vm) has no former form: it becomes a container,
-- which runs it with the container's isolation, so roll back only before such nodes are authored.
UPDATE exercise_versions v
SET variants = (SELECT COALESCE(jsonb_agg(
                           CASE WHEN jsonb_typeof(x.variant #> '{topology,devices}') = 'array' THEN
                               jsonb_set(x.variant, '{topology,devices}',
                                         (SELECT COALESCE(jsonb_agg(
                                                    CASE WHEN d.device ->> 'type' = 'node'
                                                        THEN (d.device - 'profile') || '{"type": "container"}'::jsonb
                                                        ELSE d.device END ORDER BY d.ord), '[]'::jsonb)
                                          FROM jsonb_array_elements(x.variant #> '{topology,devices}') WITH ORDINALITY AS d(device, ord)))
                           ELSE x.variant END ORDER BY x.ord), '[]'::jsonb)
                FROM jsonb_array_elements(v.variants) WITH ORDINALITY AS x(variant, ord))
WHERE jsonb_typeof(v.variants) = 'array'
  AND jsonb_path_exists(v.variants, '$[*].topology.devices[*] ? (@.type == "node")');
