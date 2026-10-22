-- name: GetActiveEventLiveScreenLink :one
-- The screen's one working link (unrevoked and not expired).
SELECT *
FROM event_live_screen_links
WHERE event_id = sqlc.arg(event_id)
  AND screen_id = sqlc.arg(screen_id)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: ListActiveEventLiveScreenLinks :many
-- The working links of all screens of the event (at most one per screen).
SELECT *
FROM event_live_screen_links
WHERE event_id = sqlc.arg(event_id)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: GetEventLiveScreenLinkByTokenHash :one
SELECT *
FROM event_live_screen_links
WHERE token_hash = $1;

-- name: IssueEventLiveScreenLink :exec
-- Creation and regeneration in one statement: any unrevoked link of the
-- screen (active or expired) is revoked and the new one inserted.
WITH revoked AS (
    UPDATE event_live_screen_links AS old
    SET revoked_at = sqlc.arg(created_at)
    WHERE old.screen_id = sqlc.arg(screen_id)
      AND old.revoked_at IS NULL
    RETURNING old.id
)
INSERT INTO event_live_screen_links (id, event_id, screen_id, token_hash, created_at, created_by, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(screen_id), sqlc.arg(token_hash), sqlc.arg(created_at), sqlc.arg(created_by), sqlc.narg(expires_at));

-- name: RevokeEventLiveScreenLinks :execrows
-- «Вимкнути»: the screen has no working link afterwards.
UPDATE event_live_screen_links
SET revoked_at = sqlc.arg(revoked_at)
WHERE event_id = sqlc.arg(event_id)
  AND screen_id = sqlc.arg(screen_id)
  AND revoked_at IS NULL;

-- name: ListActiveLiveScreenTokenHashes :many
-- The token hashes of every working link: the screen limiter gives only
-- these links their own request budget.
SELECT token_hash, expires_at
FROM event_live_screen_links
WHERE revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));
