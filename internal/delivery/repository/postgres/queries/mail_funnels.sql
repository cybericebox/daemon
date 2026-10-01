-- Outcome funnels of the mail sections, from data that already exists (no
-- tracking of opens or clicks). One statement serves both scopes: event_id
-- narrows it to one Event, NULL reads every Event. Participants of the
-- moderators team and of hidden teams are never counted. The window applies to
-- when the invitation, application or account was created.

-- name: GetMailFunnels :one
-- Invitations: sent = invitation rows, accepted = approved by the invitee;
-- declined and revoked invitations are deleted, so they do not appear. The
-- accept time runs from the last send (or the creation) to the acceptance.
-- Applications: joins that went through a decision (open registration is
-- approved on the spot and is not an application). Registration: invitee
-- accounts created by the invitation itself (within a minute of it), and how
-- many of them finished the registration; accounts purged unconfirmed are gone.
WITH scope AS (
    SELECT p.user_id, p.status, p.invited, p.created_at, p.decided_at, p.decided_by, p.invitation_sent_at,
           u.status AS user_status, u.created_at AS user_created_at
    FROM event_participants p
    JOIN users u ON u.id = p.user_id
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE (sqlc.narg(event_id)::uuid IS NULL OR p.event_id = sqlc.narg(event_id)::uuid)
      AND NOT COALESCE(t.moderators, false)
      AND NOT COALESCE(t.hidden, false)
      AND p.created_at >= COALESCE(sqlc.narg(from_at)::timestamptz, '-infinity'::timestamptz)
      AND p.created_at < COALESCE(sqlc.narg(to_at)::timestamptz, 'infinity'::timestamptz)
), inv AS (
    SELECT * FROM scope WHERE invited
), app AS (
    SELECT * FROM scope WHERE NOT invited AND (status = 1 OR decided_by IS NOT NULL)
)
SELECT (SELECT count(*) FROM inv)::bigint                                                     AS invitations_sent,
       (SELECT count(*) FROM inv WHERE status = 2)::bigint                                    AS invitations_accepted,
       (SELECT count(*) FROM inv WHERE status = 2 AND decided_at IS NOT NULL)::bigint         AS invitation_accept_samples,
       COALESCE((SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY GREATEST(0, extract(epoch FROM decided_at - COALESCE(invitation_sent_at, created_at))))
                 FROM inv WHERE status = 2 AND decided_at IS NOT NULL), 0)::float8            AS invitation_accept_median_seconds,
       (SELECT count(*) FROM inv WHERE abs(extract(epoch FROM created_at - user_created_at)) < 60)::bigint AS registrations_started,
       (SELECT count(*) FROM inv WHERE abs(extract(epoch FROM created_at - user_created_at)) < 60
                                   AND user_status <> 'incomplete')::bigint                  AS registrations_completed,
       (SELECT count(*) FROM app)::bigint                                                     AS applications_submitted,
       (SELECT count(*) FROM app WHERE status = 2)::bigint                                    AS applications_approved,
       (SELECT count(*) FROM app WHERE status = 3)::bigint                                    AS applications_rejected,
       (SELECT count(*) FROM app WHERE status IN (2, 3) AND decided_at IS NOT NULL)::bigint   AS application_decision_samples,
       COALESCE((SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY GREATEST(0, extract(epoch FROM decided_at - created_at)))
                 FROM app WHERE status IN (2, 3) AND decided_at IS NOT NULL), 0)::float8      AS application_decision_median_seconds;

-- name: GetPlatformAccountFunnel :one
-- Accounts created in the window and how many finished the registration
-- (status other than incomplete). Accounts purged unconfirmed no longer exist.
SELECT count(*)::bigint                                          AS started,
       (count(*) FILTER (WHERE status <> 'incomplete'))::bigint  AS completed
FROM users
WHERE deleted_at IS NULL
  AND created_at >= sqlc.arg(from_at)::timestamptz
  AND created_at < sqlc.arg(to_at)::timestamptz;
