-- name: CreateEventChallengeGroup :one
INSERT INTO event_challenge_groups (id, event_id, name, order_index, created_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(name), sqlc.arg(order_index), sqlc.arg(created_at))
RETURNING *;

-- name: ListEventChallengeGroups :many
SELECT *
FROM event_challenge_groups
WHERE event_id = sqlc.arg(event_id)
ORDER BY order_index ASC, id ASC;

-- name: UpdateEventChallengeGroup :one
UPDATE event_challenge_groups
SET name = sqlc.arg(name),
    order_index = sqlc.arg(order_index)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
RETURNING *;

-- name: DeleteEventChallengeGroup :execrows
DELETE FROM event_challenge_groups
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: VacateEventChallengeGroupOrders :exec
UPDATE event_challenge_groups
SET order_index = -order_index - 1
WHERE event_id = sqlc.arg(event_id);

-- name: SetEventChallengeGroupOrder :execrows
UPDATE event_challenge_groups
SET order_index = sqlc.arg(order_index)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);
