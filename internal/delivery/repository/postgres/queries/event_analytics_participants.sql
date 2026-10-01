-- Event analytics, «Учасники» and «Комунікації» sections
-- (docs/EVENT-ANALYTICS.md §6.2, §6.7). The moderators team is never counted.

-- name: GetEventParticipantFunnel :one
-- The participation funnel: invited → registered → approved → in a team →
-- first attempt → first solve. Registered excludes invitations not accepted
-- yet; a solve is an effectively correct attempt of the participant.
WITH participants AS (
    SELECT p.user_id, p.status, p.invited, p.team_id
    FROM event_participants p
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE p.event_id = sqlc.arg(event_id)
      AND NOT COALESCE(t.moderators, false)
), attempters AS (
    SELECT a.user_id, bool_or(a.effective_correct) AS solved
    FROM effective_challenge_attempts a
    JOIN event_teams t ON t.id = a.event_team_id AND NOT t.moderators
    WHERE a.event_id = sqlc.arg(event_id)
    GROUP BY a.user_id
)
SELECT (SELECT count(*) FROM participants WHERE invited)::bigint                                  AS invited,
       (SELECT count(*) FROM participants WHERE NOT (invited AND status = 1))::bigint              AS registered,
       (SELECT count(*) FROM participants WHERE status = 2)::bigint                                AS approved,
       (SELECT count(*) FROM participants WHERE status = 2 AND team_id IS NOT NULL)::bigint        AS in_team,
       (SELECT count(*) FROM attempters a WHERE a.user_id IN (SELECT user_id FROM participants))::bigint AS attempted,
       (SELECT count(*) FROM attempters a WHERE a.solved AND a.user_id IN (SELECT user_id FROM participants))::bigint AS solved;

-- name: ListEventRegistrationDays :many
-- Registrations per UTC day and channel: invitation (invited by staff), open
-- (approved on the spot) and approval (went through a decision).
SELECT (date_trunc('day', p.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day,
       CASE WHEN p.invited THEN 'invitation'
            WHEN p.status = 2 AND p.decided_by IS NULL THEN 'open'
            ELSE 'approval' END::text                                                       AS channel,
       count(*)::bigint                                                                     AS registrations
FROM event_participants p
LEFT JOIN event_teams t ON t.id = p.team_id
WHERE p.event_id = sqlc.arg(event_id)
  AND NOT COALESCE(t.moderators, false)
  AND p.created_at >= COALESCE(sqlc.narg(from_at)::timestamptz, '-infinity'::timestamptz)
  AND p.created_at < COALESCE(sqlc.narg(to_at)::timestamptz, 'infinity'::timestamptz)
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: ListEventTeamFill :many
-- Every team with its member count, the admission verdict and the invitees
-- who were invited into it and have not joined yet.
SELECT t.id,
       COALESCE(event_team_public_name(t.individual, t.event_id, t.captain_id, t.name), '')::text AS name,
       t.member_count,
       t.individual,
       event_team_admitted(t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)::boolean AS admitted,
       (SELECT count(*)
        FROM event_participants p
        WHERE p.event_id = t.event_id AND p.invited_team_id = t.id AND p.team_id IS DISTINCT FROM t.id)::bigint AS pending_invitees
FROM event_teams t
WHERE t.event_id = sqlc.arg(event_id)
  AND NOT t.moderators
ORDER BY t.member_count, t.created_at, t.id;

-- name: ListEventRegistrationFormVersions :many
-- Versions of the event's registration form (at most one form per event).
SELECT v.id, v.version, v.document
FROM event_forms f
JOIN event_form_versions v ON v.form_id = f.id
WHERE f.event_id = sqlc.arg(event_id)
  AND f.purpose = 'registration'
ORDER BY v.version;

-- name: ListEventRegistrationAnswers :many
-- The latest registration answers of every participant outside the
-- moderators team.
SELECT DISTINCT ON (a.user_id) a.form_version_id, a.answers
FROM event_form_answers a
JOIN event_form_versions v ON v.id = a.form_version_id
JOIN event_forms f ON f.id = v.form_id
LEFT JOIN event_participants p ON p.event_id = a.event_id AND p.user_id = a.user_id
LEFT JOIN event_teams t ON t.id = p.team_id
WHERE a.event_id = sqlc.arg(event_id)
  AND f.purpose = 'registration'
  AND NOT COALESCE(t.moderators, false)
ORDER BY a.user_id, a.submitted_at DESC;

-- name: ListEventDropOffParticipants :many
-- Approved participants who never submitted an attempt, with how many tasks
-- they opened. total is the full count; the list is cut at row_limit.
SELECT p.user_id,
       person.first_name,
       person.last_name,
       person.email,
       event_participant_public_name(p.event_id, p.user_id)::text                                   AS display_name,
       COALESCE(event_team_public_name(t.individual, t.event_id, t.captain_id, t.name), '')::text AS team_name,
       p.created_at                                                                                 AS registered_at,
       p.decided_at,
       (SELECT count(*) FROM event_activity o
        WHERE o.event_id = p.event_id AND o.user_id = p.user_id AND o.kind = 'task_opened')::bigint AS opened_tasks,
       count(*) OVER ()::bigint                                                                     AS total
FROM event_participants p
JOIN users person ON person.id = p.user_id
LEFT JOIN event_teams t ON t.id = p.team_id
WHERE p.event_id = sqlc.arg(event_id)
  AND p.status = 2
  AND NOT COALESCE(t.moderators, false)
  AND NOT EXISTS (SELECT 1 FROM challenge_attempts a WHERE a.event_id = p.event_id AND a.user_id = p.user_id)
ORDER BY p.created_at, p.user_id
LIMIT sqlc.arg(row_limit);

-- name: ListEventNotificationDispatchStats :many
-- Dispatch targets of the event's notifications per type, channel and
-- status (done: handed to the transport, error: failed) in the window.
SELECT d.notification_type,
       tg.channel,
       tg.status,
       count(*)::bigint AS targets
FROM notification_dispatches d
JOIN notification_dispatch_targets tg ON tg.dispatch_id = d.id
WHERE d.scope_event_id = sqlc.arg(event_id)
  AND d.created_at >= COALESCE(sqlc.narg(from_at)::timestamptz, '-infinity'::timestamptz)
  AND d.created_at < COALESCE(sqlc.narg(to_at)::timestamptz, 'infinity'::timestamptz)
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- name: ListEventInAppStats :many
-- In-app notifications of the event per type: how many, how many read.
SELECT COALESCE(n.notification_type, '')::text AS notification_type,
       count(*)::bigint                        AS total,
       (count(n.read_at))::bigint              AS read
FROM in_app_notifications n
WHERE n.scope_event_id = sqlc.arg(event_id)
  AND n.created_at >= COALESCE(sqlc.narg(from_at)::timestamptz, '-infinity'::timestamptz)
  AND n.created_at < COALESCE(sqlc.narg(to_at)::timestamptz, 'infinity'::timestamptz)
GROUP BY 1
ORDER BY 1;

-- name: ListEventFormCompletion :many
-- Per form: deliveries assigned and completed in the window, and the answers
-- submitted (the registration form is answered without a delivery).
SELECT f.id,
       f.title,
       f.purpose,
       f.enabled,
       (SELECT count(*)
        FROM event_form_deliveries d
        JOIN event_form_versions v ON v.id = d.form_version_id
        WHERE v.form_id = f.id
          AND d.created_at >= COALESCE(sqlc.narg(from_at)::timestamptz, '-infinity'::timestamptz)
          AND d.created_at < COALESCE(sqlc.narg(to_at)::timestamptz, 'infinity'::timestamptz))::bigint AS assigned,
       (SELECT count(*)
        FROM event_form_deliveries d
        JOIN event_form_versions v ON v.id = d.form_version_id
        WHERE v.form_id = f.id
          AND d.completed_at IS NOT NULL
          AND d.created_at >= COALESCE(sqlc.narg(from_at)::timestamptz, '-infinity'::timestamptz)
          AND d.created_at < COALESCE(sqlc.narg(to_at)::timestamptz, 'infinity'::timestamptz))::bigint AS completed,
       (SELECT count(*)
        FROM event_form_answers a
        JOIN event_form_versions v ON v.id = a.form_version_id
        WHERE v.form_id = f.id
          AND a.submitted_at >= COALESCE(sqlc.narg(from_at)::timestamptz, '-infinity'::timestamptz)
          AND a.submitted_at < COALESCE(sqlc.narg(to_at)::timestamptz, 'infinity'::timestamptz))::bigint AS answers
FROM event_forms f
WHERE f.event_id = sqlc.arg(event_id)
ORDER BY (f.purpose = 'registration') DESC, f.created_at, f.id;
