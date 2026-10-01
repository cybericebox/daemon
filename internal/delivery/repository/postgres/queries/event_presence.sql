-- name: TouchEventParticipantPresence :exec
-- A time only. The write is skipped while the stored time is under a minute
-- old, so a burst of requests costs one row update.
INSERT INTO event_participant_presence (event_id, user_id, last_seen_at)
VALUES (sqlc.arg(event_id), sqlc.arg(user_id), sqlc.arg(seen_at))
ON CONFLICT (event_id, user_id) DO UPDATE
    SET last_seen_at = EXCLUDED.last_seen_at
WHERE event_participant_presence.last_seen_at < EXCLUDED.last_seen_at - interval '1 minute';

-- name: ListEventUsersLastActivity :many
-- The event presence and the last lab access of the given users; users with
-- neither are absent from the result.
SELECT u.user_id::uuid                                                    AS user_id,
       presence.last_seen_at                                              AS last_seen_at,
       COALESCE(lab.at, 'epoch'::timestamptz)::timestamptz AS last_lab_at
FROM unnest(sqlc.arg(user_ids)::uuid[]) AS u(user_id)
         LEFT JOIN LATERAL (SELECT event_user_last_lab_at(sqlc.arg(event_id)::uuid, u.user_id) AS at) lab ON true
         LEFT JOIN event_participant_presence presence
                   ON presence.event_id = sqlc.arg(event_id)::uuid AND presence.user_id = u.user_id;
