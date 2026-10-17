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
