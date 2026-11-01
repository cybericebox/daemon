-- name: DeleteExpiredGoogleLinkTickets :exec
DELETE
FROM google_link_tickets
WHERE expires_at <= sqlc.arg(now);

-- name: ReplaceGoogleLinkTicket :exec
-- One ticket per (account, Google identity): a new sign-in replaces the earlier one (fresh ticket, no code, new life).
INSERT INTO google_link_tickets (id, ticket_hash, user_id, google_id, google_email, google_name, return_to, created_at,
                                expires_at)
VALUES (sqlc.arg(id), sqlc.arg(ticket_hash), sqlc.arg(user_id), sqlc.arg(google_id), sqlc.arg(google_email),
        sqlc.arg(google_name), sqlc.arg(return_to), sqlc.arg(created_at), sqlc.arg(expires_at))
ON CONFLICT (user_id, google_id) DO UPDATE SET id            = EXCLUDED.id,
                                               ticket_hash   = EXCLUDED.ticket_hash,
                                               google_email  = EXCLUDED.google_email,
                                               google_name   = EXCLUDED.google_name,
                                               return_to     = EXCLUDED.return_to,
                                               code_hash     = '',
                                               code_sent_at  = NULL,
                                               code_attempts = 0,
                                               created_at    = EXCLUDED.created_at,
                                               expires_at    = EXCLUDED.expires_at;

-- name: GetGoogleLinkTicket :one
SELECT *
FROM google_link_tickets
WHERE ticket_hash = sqlc.arg(ticket_hash)
  AND expires_at > sqlc.arg(now);

-- name: SetGoogleLinkTicketCode :execrows
-- A fresh mailed code replaces the earlier one and has all its attempts again.
UPDATE google_link_tickets
SET code_hash     = sqlc.arg(code_hash),
    code_sent_at  = sqlc.arg(sent_at),
    code_attempts = 0
WHERE id = sqlc.arg(id)
  AND expires_at > sqlc.arg(sent_at);

-- name: CountGoogleLinkTicketCodeAttempt :one
-- Counts one check of the mailed code and returns how many were made; zero rows: no attempts are left.
UPDATE google_link_tickets
SET code_attempts = code_attempts + 1
WHERE id = sqlc.arg(id)
  AND expires_at > sqlc.arg(now)
  AND code_attempts < sqlc.arg(max_attempts)
RETURNING code_attempts;

-- name: ConsumeGoogleLinkTicket :execrows
-- The delete is the claim: of two concurrent confirmations only one removes the row.
DELETE
FROM google_link_tickets
WHERE id = sqlc.arg(id)
  AND expires_at > sqlc.arg(now);

-- name: DeleteGoogleLinkTicket :execrows
DELETE
FROM google_link_tickets
WHERE id = sqlc.arg(id);
