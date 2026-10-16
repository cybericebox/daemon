-- name: GetEventPreviewSettings :one
SELECT selections,group_name FROM event_preview_settings WHERE event_id = $1;
-- name: UpsertEventPreviewSettings :exec
INSERT INTO event_preview_settings(event_id,selections,group_name,updated_at) VALUES(sqlc.arg(event_id),sqlc.narg(selections),sqlc.narg(group_name),sqlc.arg(updated_at))
ON CONFLICT(event_id) DO UPDATE SET selections=EXCLUDED.selections,group_name=EXCLUDED.group_name,updated_at=EXCLUDED.updated_at;
-- name: DeleteEventPreviewSettings :exec
DELETE FROM event_preview_settings WHERE event_id=$1;
-- name: LockEventPreviewOwner :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 739221));
-- name: GetEventPreviewRun :one
SELECT * FROM event_preview_runs WHERE event_id=$1 AND user_id=$2 AND selection_id=$3;
-- name: UpsertEventPreviewRun :exec
INSERT INTO event_preview_runs(id,event_id,user_id,selection_id,deploy_id,state,created_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(event_id,user_id,selection_id) DO UPDATE SET id=EXCLUDED.id,deploy_id=EXCLUDED.deploy_id,state=EXCLUDED.state,created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at;
-- name: DeleteEventPreviewRun :exec
DELETE FROM event_preview_runs WHERE event_id=$1 AND user_id=$2 AND selection_id=$3;
-- name: ListEventPreviewRuns :many
SELECT * FROM event_preview_runs WHERE event_id=$1 AND user_id=$2;
