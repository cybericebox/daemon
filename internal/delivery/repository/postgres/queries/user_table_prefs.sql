-- name: GetUserTablePrefs :one
SELECT prefs, updated_at
FROM user_table_prefs
WHERE user_id = $1
  AND table_key = $2;

-- name: UpsertUserTablePrefs :execrows
-- Refuses (no row) a new table key once the person holds max_tables of them; changing a known one always works.
INSERT INTO user_table_prefs (user_id, table_key, prefs, updated_at)
SELECT sqlc.arg(user_id), sqlc.arg(table_key), sqlc.arg(prefs), sqlc.arg(updated_at)
WHERE EXISTS (SELECT 1 FROM user_table_prefs WHERE user_id = sqlc.arg(user_id) AND table_key = sqlc.arg(table_key))
   OR (SELECT count(*) FROM user_table_prefs WHERE user_id = sqlc.arg(user_id)) < sqlc.arg(max_tables)::bigint
ON CONFLICT (user_id, table_key) DO UPDATE SET prefs = EXCLUDED.prefs, updated_at = EXCLUDED.updated_at;

-- name: DeleteUserTablePrefs :execrows
DELETE
FROM user_table_prefs
WHERE user_id = $1
  AND table_key = $2;

-- name: DeleteAllUserTablePrefs :execrows
DELETE
FROM user_table_prefs
WHERE user_id = $1;

-- name: ExportUserTablePrefs :many
SELECT table_key, prefs, updated_at
FROM user_table_prefs
WHERE user_id = $1
ORDER BY table_key;
