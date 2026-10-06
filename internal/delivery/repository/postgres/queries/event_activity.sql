-- Event activity log (docs/EVENT-ANALYTICS.md §3). Append-only: rows are
-- written, never updated, except the retention anonymization.

-- name: CreateEventActivity :exec
INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at, data)
VALUES (sqlc.arg(event_id), sqlc.arg(user_id), sqlc.narg(team_id), sqlc.arg(kind), sqlc.narg(subject_id), sqlc.arg(at), sqlc.arg(data));

-- name: CreateEventActivityOnce :execrows
-- Writes the row unless the user already has one of the same kind for the
-- same subject at or after since (the dedupe window of task opens).
INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at, data)
SELECT sqlc.arg(event_id)::uuid, sqlc.arg(user_id)::uuid, sqlc.narg(team_id)::uuid, sqlc.arg(kind)::text,
       sqlc.arg(subject_id)::uuid, sqlc.arg(at)::timestamptz, sqlc.arg(data)::jsonb
WHERE NOT EXISTS (SELECT 1
                  FROM event_activity a
                  WHERE a.event_id = sqlc.arg(event_id)::uuid
                    AND a.subject_id = sqlc.arg(subject_id)::uuid
                    AND a.kind = sqlc.arg(kind)::text
                    AND a.user_id = sqlc.arg(user_id)::uuid
                    AND a.at >= sqlc.arg(since)::timestamptz);
