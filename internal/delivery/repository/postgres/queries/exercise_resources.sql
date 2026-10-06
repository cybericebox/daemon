-- name: ListPublishedVariantDevices :many
-- What the resource totals of a page of exercises need from their published versions: per variant its id and
-- the container-device fields that decide a device's size (never the image, env vars or secrets).
SELECT e.id AS exercise_id,
       COALESCE((SELECT jsonb_agg(jsonb_build_object(
                           'id', variant -> 'id',
                           'topology', jsonb_build_object(
                                   'devices', COALESCE((SELECT jsonb_agg(jsonb_build_object(
                                                                    'id', device -> 'id',
                                                                    'name', device -> 'name',
                                                                    'type', device -> 'type',
                                                                    'resource_preset', device -> 'resource_preset',
                                                                    'resources', device -> 'resources'))
                                                        FROM jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) AS device),
                                                       '[]'::jsonb))))
                 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(version.variants) = 'array' THEN version.variants ELSE '[]'::jsonb END) AS variant), '[]'::jsonb)::jsonb AS variants
FROM exercises e
         JOIN exercise_versions version ON version.id = e.published_version_id
WHERE e.id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ListVersionVariantDevices :many
-- The same reduced variants for specific versions (an event pins versions, not "the published one").
SELECT version.id          AS version_id,
       version.exercise_id AS exercise_id,
       COALESCE((SELECT jsonb_agg(jsonb_build_object(
                           'id', variant -> 'id',
                           'topology', jsonb_build_object(
                                   'devices', COALESCE((SELECT jsonb_agg(jsonb_build_object(
                                                                    'id', device -> 'id',
                                                                    'name', device -> 'name',
                                                                    'type', device -> 'type',
                                                                    'resource_preset', device -> 'resource_preset',
                                                                    'resources', device -> 'resources'))
                                                        FROM jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) AS device),
                                                       '[]'::jsonb))))
                 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(version.variants) = 'array' THEN version.variants ELSE '[]'::jsonb END) AS variant), '[]'::jsonb)::jsonb AS variants
FROM exercise_versions version
WHERE version.id = ANY (sqlc.arg(ids)::uuid[]);
