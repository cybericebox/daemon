-- The device type "container" becomes the type "node" with the profile "container" (profiles: container, microvm,
-- vm). Only exercise_versions.variants stores device types; event and team copies pin tasks, not topologies.
-- Exercise writes pass the catalog_metadata_writer fence of 0195 through the connection parameter of the migration.
UPDATE exercise_versions v
SET variants = (SELECT COALESCE(jsonb_agg(
                           CASE WHEN jsonb_typeof(x.variant #> '{topology,devices}') = 'array' THEN
                               jsonb_set(x.variant, '{topology,devices}',
                                         (SELECT COALESCE(jsonb_agg(
                                                    CASE WHEN d.device ->> 'type' = 'container'
                                                        THEN d.device || '{"type": "node", "profile": "container"}'::jsonb
                                                        ELSE d.device END ORDER BY d.ord), '[]'::jsonb)
                                          FROM jsonb_array_elements(x.variant #> '{topology,devices}') WITH ORDINALITY AS d(device, ord)))
                           ELSE x.variant END ORDER BY x.ord), '[]'::jsonb)
                FROM jsonb_array_elements(v.variants) WITH ORDINALITY AS x(variant, ord))
WHERE jsonb_typeof(v.variants) = 'array'
  AND jsonb_path_exists(v.variants, '$[*].topology.devices[*] ? (@.type == "container")');
