-- name: GetEventTeamFieldConfig :one
SELECT * FROM event_team_field_configs WHERE event_id = sqlc.arg(event_id);

-- name: UpsertEventTeamFieldConfig :one
INSERT INTO event_team_field_configs (event_id, version, enabled, required, document, updated_at, require_existing, block_submissions)
VALUES (sqlc.arg(event_id), 1, sqlc.arg(enabled), sqlc.arg(required), sqlc.arg(document), sqlc.arg(updated_at),
        sqlc.arg(require_existing), sqlc.arg(block_submissions))
ON CONFLICT (event_id) DO UPDATE SET
    version = event_team_field_configs.version + 1,
    enabled = EXCLUDED.enabled,
    required = EXCLUDED.required,
    document = EXCLUDED.document,
    updated_at = EXCLUDED.updated_at,
    require_existing = EXCLUDED.require_existing,
    block_submissions = EXCLUDED.block_submissions
RETURNING *;

-- name: UpdateEventTeamExtraFields :execrows
-- fields_missing is written together with the answers when known (NULL keeps it).
UPDATE event_teams SET extra_fields = sqlc.arg(extra_fields),
    fields_missing = COALESCE(sqlc.narg(fields_missing)::int, fields_missing)
WHERE event_id = sqlc.arg(event_id) AND id = sqlc.arg(team_id);

-- name: GetEventTeamExtraFields :one
SELECT extra_fields FROM event_teams
WHERE event_id = sqlc.arg(event_id) AND id = sqlc.arg(team_id);
