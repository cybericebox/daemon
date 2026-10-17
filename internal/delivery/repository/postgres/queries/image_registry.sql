-- name: GetImageRegistrySettings :one
SELECT *
FROM image_registry_settings
WHERE id;

-- name: UpsertImageRegistrySettings :exec
-- The settings are one row (id is always true): the whole entity is written in
-- one statement. Timestamps come from the domain.
INSERT INTO image_registry_settings (id, provider, account, repository, push_token_ciphertext, pull_token_ciphertext, updated_at)
VALUES (true, $1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE
    SET provider              = EXCLUDED.provider,
        account               = EXCLUDED.account,
        repository            = EXCLUDED.repository,
        push_token_ciphertext = EXCLUDED.push_token_ciphertext,
        pull_token_ciphertext = EXCLUDED.pull_token_ciphertext,
        updated_at            = EXCLUDED.updated_at;

-- name: DeleteImageRegistrySettings :exec
DELETE
FROM image_registry_settings;

-- name: ListEventExerciseImages :many
-- The active attachments of an event with the distinct device images of their
-- pinned version (every variant's topology; forwarding devices carry no
-- image). A read-side shape for the image availability check.
SELECT ee.id                                            AS event_exercise_id,
       ee.exercise_id,
       ex.name::text                                    AS exercise_name,
       ARRAY(SELECT DISTINCT device ->> 'image'
             FROM jsonb_array_elements(CASE WHEN jsonb_typeof(pinned.variants) = 'array' THEN pinned.variants ELSE '[]'::jsonb END) AS variant,
                  jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) AS device
             WHERE COALESCE(device ->> 'image', '') <> ''
             ORDER BY 1)::text[]                        AS images
FROM event_exercises ee
JOIN exercises ex ON ex.id = ee.exercise_id
JOIN exercise_versions pinned ON pinned.id = ee.exercise_version_id
WHERE ee.event_id = sqlc.arg(event_id)
  AND ee.status = 0
ORDER BY ee.created_at ASC, ee.id ASC;
