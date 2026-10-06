-- Catalog test labs (exercise test deploys) on the platform infrastructure, for
-- the admin labs page, the dashboard and the infrastructure analytics. A row of
-- exercise_test_deployments lives while the lab is leased; the row is removed
-- when the lab is stopped or its expired lease is cleaned up.

-- name: ListPlatformTestLabs :many
SELECT d.id, d.group_name, d.lab_name, d.variant_id, d.created_by, d.created_at, d.expires_at,
       u.first_name AS author_first_name, u.last_name AS author_last_name, u.email AS author_email,
       v.exercise_id, ex.name AS exercise_name,
       -- The 1-based position of the variant in its version: the number the catalog shows («Варіант N»).
       COALESCE((SELECT x.ord
                 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(v.variants) = 'array' THEN v.variants ELSE '[]'::jsonb END) WITH ORDINALITY AS x(value, ord)
                 WHERE x.value ->> 'id' = d.variant_id::text
                 LIMIT 1), 0)::int AS variant_number,
       count(*) OVER ()::bigint AS total
FROM exercise_test_deployments d
JOIN exercise_versions v ON v.id = d.version_id
JOIN exercises ex ON ex.id = v.exercise_id
JOIN users u ON u.id = d.created_by
WHERE (sqlc.arg(search)::text = ''
    OR ex.name ILIKE '%' || sqlc.arg(search)::text || '%'
    OR u.email ILIKE '%' || sqlc.arg(search)::text || '%'
    OR (u.first_name || ' ' || u.last_name) ILIKE '%' || sqlc.arg(search)::text || '%')
ORDER BY d.created_at DESC, d.id
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: GetPlatformTestLab :one
-- devices: the logical devices (id, name, type, never images or variables) of the topology the lab was deployed from.
SELECT d.id, d.group_name, d.lab_name, d.created_by,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('id', device.value -> 'id', 'name', device.value -> 'name', 'type', device.value -> 'type') ORDER BY device.ord)
                 FROM exercise_versions v,
                      jsonb_array_elements(CASE WHEN jsonb_typeof(v.variants) = 'array' THEN v.variants ELSE '[]'::jsonb END) AS variant,
                      jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) WITH ORDINALITY AS device(value, ord)
                 WHERE v.id = d.version_id AND variant ->> 'id' = d.variant_id::text), '[]'::jsonb)::jsonb AS devices
FROM exercise_test_deployments d WHERE d.id = sqlc.arg(id);

-- name: CountPlatformTestLabs :one
-- active = lease not over yet, expired = the lease is over but the lab is not cleaned up yet.
SELECT count(*) FILTER (WHERE expires_at > sqlc.arg(now))::bigint  AS active,
       count(*) FILTER (WHERE expires_at <= sqlc.arg(now))::bigint AS expired
FROM exercise_test_deployments;
