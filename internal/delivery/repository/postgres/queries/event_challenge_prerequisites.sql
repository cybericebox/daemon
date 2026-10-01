-- name: ListEventChallengePrerequisites :many
SELECT prerequisite_challenge_id
FROM event_challenge_prerequisites
WHERE challenge_id = sqlc.arg(challenge_id)
ORDER BY prerequisite_challenge_id ASC;

-- name: DeleteEventChallengePrerequisites :exec
DELETE FROM event_challenge_prerequisites
WHERE challenge_id = sqlc.arg(challenge_id);

-- name: CreateEventChallengePrerequisite :exec
INSERT INTO event_challenge_prerequisites (challenge_id, prerequisite_challenge_id)
VALUES (sqlc.arg(challenge_id), sqlc.arg(prerequisite_challenge_id));
