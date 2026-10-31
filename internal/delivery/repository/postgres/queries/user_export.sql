-- The "download my data" export. Every query is keyed on the caller's own user id and reads only that person's
-- rows; names of other people never leave (a team shows only its title).

-- name: ExportUserParticipations :many
SELECT e.id                                  AS event_id,
       e.name                                AS event_name,
       e.tag                                 AS event_tag,
       p.status,
       p.created_at,
       p.decided_at,
       p.invited,
       p.invited_to_team,
       p.invitation_sent_at,
       COALESCE(t.name, '')::text            AS team_name,
       COALESCE(it.name, '')::text           AS invited_team_name,
       p.team_role,
       COALESCE(t.captain_id = p.user_id, false)::boolean AS is_captain,
       COALESCE(p.pseudonym, '')::text       AS pseudonym
FROM event_participants p
         JOIN events e ON e.id = p.event_id
         LEFT JOIN event_teams t ON t.id = p.team_id
         LEFT JOIN event_teams it ON it.id = p.invited_team_id
WHERE p.user_id = $1
ORDER BY p.created_at, e.id;

-- name: ExportUserEventStaffRoles :many
SELECT e.id AS event_id, e.name AS event_name, m.role, m.created_at
FROM event_managers m
         JOIN events e ON e.id = m.event_id
WHERE m.user_id = $1
ORDER BY m.created_at, e.id;

-- name: ExportUserFlagAttempts :many
SELECT a.id,
       e.id                                            AS event_id,
       e.name                                          AS event_name,
       COALESCE(t.name, '')::text                      AS team_name,
       COALESCE(ec.snapshot ->> 'name', '')::text      AS challenge_name,
       a.answer,
       a.correct,
       a.practice,
       a.received_at
FROM challenge_attempts a
         JOIN events e ON e.id = a.event_id
         LEFT JOIN event_teams t ON t.id = a.event_team_id
         LEFT JOIN team_challenges tc ON tc.id = a.team_challenge_id
         LEFT JOIN event_challenges ec ON ec.id = tc.event_challenge_id
WHERE a.user_id = $1
ORDER BY a.received_at, a.id
LIMIT 20000;

-- name: ExportUserFormAnswers :many
SELECT e.id AS event_id, e.name AS event_name, f.form_version_id, f.answers, f.submitted_at
FROM event_form_answers f
         JOIN events e ON e.id = f.event_id
WHERE f.user_id = $1
ORDER BY f.submitted_at, e.id;

-- name: ExportUserInbox :many
SELECT n.title, n.body, n.link, n.notification_type, n.category, n.created_at, n.read_at,
       COALESCE(e.name, '')::text AS event_name
FROM in_app_notifications n
         LEFT JOIN events e ON e.id = n.scope_event_id
WHERE n.user_id = $1
  AND n.surface = 'inbox'
ORDER BY n.created_at, n.id
LIMIT 5000;

-- name: ExportUserNotificationSettings :many
SELECT notification_type, channel, enabled
FROM notification_user_settings
WHERE user_id = $1
ORDER BY notification_type, channel;
