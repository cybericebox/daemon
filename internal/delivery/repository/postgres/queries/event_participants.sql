-- name: UpsertEventParticipant :one
-- Join creates the row; a conflicting (event,user) row is left untouched and
-- returned (the use case decides whether that is ErrAlreadyParticipant).
INSERT INTO event_participants (event_id, user_id, status, created_at, decided_at, decided_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id, user_id) DO NOTHING
RETURNING *;

-- name: GetEventParticipant :one
SELECT *
FROM event_participants
WHERE event_id = $1
  AND user_id = $2;

-- name: InviteEventParticipant :one
INSERT INTO event_participants (event_id, user_id, status, created_at, invited_by, invited, invited_team_id, invited_to_team)
VALUES (sqlc.arg(event_id), sqlc.arg(user_id), 1, sqlc.arg(created_at), sqlc.arg(invited_by), true,
        sqlc.narg(invited_team_id)::uuid, sqlc.narg(invited_team_id)::uuid IS NOT NULL)
ON CONFLICT (event_id, user_id) DO NOTHING
RETURNING *;

-- name: UpdateEventParticipant :execrows
UPDATE event_participants
SET status     = $3,
    decided_at = $4,
    decided_by = $5
WHERE event_id = $1
  AND user_id = $2
  AND status = 1;

-- name: RemoveEventParticipantFromEvent :execrows
-- Leaving the event: an approved participant becomes rejected and loses the team.
UPDATE event_participants
SET status     = 3,
    decided_at = sqlc.arg(decided_at),
    team_id    = NULL,
    team_role  = NULL
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND status = 2;

-- name: AssignEventParticipantTeam :execrows
-- Only an approved, currently unassigned participant may enter a team.
UPDATE event_participants
SET team_id   = sqlc.arg(team_id),
    team_role = sqlc.arg(team_role)
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND status = 2
  AND team_id IS NULL;

-- name: ClearEventParticipantTeam :execrows
-- A member may only be detached from the exact current team selected by the
-- use case. Captains need an explicit transfer or disband operation instead.
UPDATE event_participants
SET team_id = NULL,
    team_role = NULL
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND team_id = sqlc.arg(team_id)
  AND team_role = 1;

-- name: SetEventParticipantTeamRole :execrows
UPDATE event_participants
SET team_role = sqlc.arg(team_role)
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND team_id = sqlc.arg(team_id);

-- name: ListEventParticipantsDetailed :many
-- Moderation list: one query for the page including profile, pseudonym and
-- team names (no per-row lookups). status_filter < 0 means "any status";
-- kind '' means any, otherwise participants | applications | invitations.
-- search '' means no text filter; otherwise it matches name, email or pseudonym.
-- field_filters is a JSON array of answer filters (event_answer_matches).
SELECT participant.*,
       person.first_name,
       person.last_name,
       person.email,
       event_participant_public_name(participant.event_id, participant.user_id)::text AS display_name,
       COALESCE(team.hidden, false)::boolean                                            AS team_hidden,
       COALESCE(event_team_public_name(team.individual, team.event_id, team.captain_id, team.name), '')::text AS team_name,
       COALESCE(invited_team.name, '')::text                                            AS invited_team_name,
       presence.last_seen_at                                                            AS last_seen_at,
       COALESCE(lab.at, 'epoch'::timestamptz)::timestamptz AS last_lab_at
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
         LEFT JOIN event_teams team ON team.id = participant.team_id
         LEFT JOIN event_teams invited_team ON invited_team.id = participant.invited_team_id
         LEFT JOIN event_participant_presence presence ON presence.event_id = participant.event_id AND presence.user_id = participant.user_id
         CROSS JOIN LATERAL (SELECT event_user_last_lab_at(participant.event_id, participant.user_id) AS at) lab
WHERE participant.event_id = sqlc.arg(event_id)::uuid
  AND (sqlc.arg(status_filter)::int < 0 OR participant.status = sqlc.arg(status_filter)::int)
  AND (sqlc.arg(kind)::text = ''
    OR (sqlc.arg(kind)::text = 'participants' AND participant.status = 2)
    OR (sqlc.arg(kind)::text = 'applications' AND NOT participant.invited AND participant.status IN (1, 3))
    OR (sqlc.arg(kind)::text = 'invitations' AND participant.invited AND participant.status = 1))
  AND (sqlc.arg(search)::text = ''
    OR person.email ILIKE '%' || sqlc.arg(search)::text || '%'
    OR (NOT (participant.invited AND participant.status = 1)
        AND (person.first_name || ' ' || person.last_name) ILIKE '%' || sqlc.arg(search)::text || '%')
    OR COALESCE(participant.pseudonym, '') ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (jsonb_array_length(sqlc.arg(field_filters)::jsonb) = 0
    OR event_answers_match_all(event_registration_answers(participant.event_id, participant.user_id), sqlc.arg(field_filters)::jsonb))
  AND (participant.created_at, participant.user_id) <
      (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY participant.created_at DESC, participant.user_id DESC
LIMIT sqlc.arg(limit_val);

-- name: CountEventParticipants :one
-- Mirrors ListEventParticipantsDetailed's status, kind, search and field filters.
SELECT count(*)
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
WHERE participant.event_id = sqlc.arg(event_id)::uuid
  AND (sqlc.arg(status_filter)::int < 0 OR participant.status = sqlc.arg(status_filter)::int)
  AND (sqlc.arg(kind)::text = ''
    OR (sqlc.arg(kind)::text = 'participants' AND participant.status = 2)
    OR (sqlc.arg(kind)::text = 'applications' AND NOT participant.invited AND participant.status IN (1, 3))
    OR (sqlc.arg(kind)::text = 'invitations' AND participant.invited AND participant.status = 1))
  AND (sqlc.arg(search)::text = ''
    OR person.email ILIKE '%' || sqlc.arg(search)::text || '%'
    OR (NOT (participant.invited AND participant.status = 1)
        AND (person.first_name || ' ' || person.last_name) ILIKE '%' || sqlc.arg(search)::text || '%')
    OR COALESCE(participant.pseudonym, '') ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (jsonb_array_length(sqlc.arg(field_filters)::jsonb) = 0
    OR event_answers_match_all(event_registration_answers(participant.event_id, participant.user_id), sqlc.arg(field_filters)::jsonb));

-- name: CountEventParticipantKinds :one
-- Tab counters: applications count only undecided requests.
SELECT count(*) FILTER (WHERE status = 2)                  AS participants,
       count(*) FILTER (WHERE NOT invited AND status = 1) AS applications,
       count(*) FILTER (WHERE invited AND status = 1)     AS invitations
FROM event_participants
WHERE event_id = sqlc.arg(event_id)::uuid;

-- name: SetEventParticipantPseudonym :execrows
UPDATE event_participants
SET pseudonym = sqlc.narg(pseudonym)
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id);

-- name: DeleteEventParticipantInvitation :execrows
-- Revoke/decline: only a still pending invitation disappears.
DELETE
FROM event_participants
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND invited
  AND status = 1;

-- name: MarkEventParticipantInvitationSent :execrows
UPDATE event_participants
SET invitation_sent_at = sqlc.arg(sent_at)
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND invited
  AND status = 1;

-- name: CountPendingTeamInvitations :one
SELECT count(*)
FROM event_participants
WHERE event_id = sqlc.arg(event_id)
  AND invited_team_id = sqlc.arg(team_id)
  AND invited
  AND status = 1;

-- name: ListEventTeamMembers :many
SELECT participant.team_id::uuid AS team_id,
       participant.user_id,
       participant.team_role,
       participant.pseudonym,
       person.first_name,
       person.last_name,
       person.email,
       presence.last_seen_at                                                           AS last_seen_at,
       COALESCE(lab.at, 'epoch'::timestamptz)::timestamptz AS last_lab_at
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
         LEFT JOIN event_participant_presence presence ON presence.event_id = participant.event_id AND presence.user_id = participant.user_id
         CROSS JOIN LATERAL (SELECT event_user_last_lab_at(participant.event_id, participant.user_id) AS at) lab
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.team_id = ANY (sqlc.arg(team_ids)::uuid[])
ORDER BY participant.team_role ASC, person.first_name ASC, person.last_name ASC, participant.user_id ASC;

-- name: ListPendingTeamInvitations :many
SELECT participant.invited_team_id::uuid AS team_id,
       participant.user_id,
       participant.created_at,
       participant.invitation_sent_at,
       person.first_name,
       person.last_name,
       person.email
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.invited
  AND participant.status = 1
  AND participant.invited_team_id = ANY (sqlc.arg(team_ids)::uuid[])
ORDER BY participant.created_at ASC, participant.user_id ASC;

-- name: GetEventParticipantProfile :one
SELECT participant.pseudonym,
       person.first_name,
       person.last_name,
       event_participant_public_name(participant.event_id, participant.user_id)::text AS display_name
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.user_id = sqlc.arg(user_id);

-- name: ListOwnTeamMembers :many
-- The caller's team roster with each member's public name (pseudonym when
-- allowed and set, else the real name). Captain first, then by name.
SELECT participant.user_id,
       participant.team_role,
       event_participant_public_name(participant.event_id, participant.user_id)::text AS display_name
FROM event_participants participant
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.team_id = sqlc.arg(team_id)::uuid
ORDER BY participant.team_role ASC, display_name ASC, participant.user_id ASC;

-- name: ListOwnTeamPendingInvitees :many
-- Invitees of one team who have not accepted yet, shown to the team's members
-- under the same public name rule as members.
SELECT participant.user_id,
       event_participant_public_name(participant.event_id, participant.user_id)::text AS display_name
FROM event_participants participant
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.invited
  AND participant.status = 1
  AND participant.invited_team_id = sqlc.arg(team_id)::uuid
ORDER BY display_name ASC, participant.user_id ASC;

-- name: SetEventParticipantInvitedTeam :execrows
-- Moves a still pending event invitation to a team (moderator team builder).
UPDATE event_participants
SET invited_team_id = sqlc.arg(team_id)::uuid,
    invited_to_team = true
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND invited
  AND status = 1;

-- name: ListEventParticipantsTable :many
-- Manage table (offset mode): every column can filter and sort. A row is
-- matched as a document of its standard columns ("@name", "@status", …)
-- over its latest registration answers (loaded only when with_answers);
-- filters is a JSON array for event_answers_match_all and sort_key is a
-- document key sorted by event_list_sort_value.
SELECT participant.*,
       person.first_name,
       person.last_name,
       person.email,
       event_participant_public_name(participant.event_id, participant.user_id)::text AS display_name,
       COALESCE(team.hidden, false)::boolean                                            AS team_hidden,
       COALESCE(event_team_public_name(team.individual, team.event_id, team.captain_id, team.name), '')::text AS team_name,
       COALESCE(invited_team.name, '')::text                                            AS invited_team_name,
       presence.last_seen_at                                                            AS last_seen_at,
       COALESCE(lab.at, 'epoch'::timestamptz)::timestamptz AS last_lab_at
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
         LEFT JOIN event_teams team ON team.id = participant.team_id
         LEFT JOIN event_teams invited_team ON invited_team.id = participant.invited_team_id
         LEFT JOIN event_participant_presence presence ON presence.event_id = participant.event_id AND presence.user_id = participant.user_id
         CROSS JOIN LATERAL (SELECT event_user_last_lab_at(participant.event_id, participant.user_id) AS at) lab
         CROSS JOIN LATERAL (SELECT CASE
                                        WHEN sqlc.arg(with_answers)::boolean
                                            THEN COALESCE(event_registration_answers(participant.event_id, participant.user_id), '{}'::jsonb)
                                        ELSE '{}'::jsonb END
                                    || jsonb_build_object(
                                        -- A pending invitation is known by the address the organizer typed only:
                                        -- the profile name of an invited account is not theirs to search or sort by.
                                        '@name', CASE WHEN participant.invited AND participant.status = 1 THEN person.email
                                                      ELSE COALESCE(NULLIF(btrim(person.first_name || ' ' || person.last_name), ''),
                                                                    NULLIF(event_participant_public_name(participant.event_id, participant.user_id), ''),
                                                                    person.email) END,
                                        '@email', person.email,
                                        '@pseudonym', participant.pseudonym,
                                        '@status', participant.status::text,
                                        '@invitation', CASE WHEN participant.invitation_sent_at IS NULL THEN 'notSent' ELSE 'sent' END,
                                        '@team', CASE
                                                     WHEN participant.invited AND participant.status = 1
                                                         THEN COALESCE(participant.invited_team_id::text, 'none')
                                                     ELSE COALESCE(participant.team_id::text, 'none') END,
                                        '@teamName', CASE
                                                         WHEN participant.invited AND participant.status = 1 THEN invited_team.name
                                                         ELSE team.name END,
                                        '@missing', participant.fields_missing > 0,
                                        '@date', participant.created_at,
                                        '@lastSeen', presence.last_seen_at,
                                        '@lastLab', lab.at) AS doc) listed
WHERE participant.event_id = sqlc.arg(event_id)::uuid
  AND (sqlc.arg(status_filter)::int < 0 OR participant.status = sqlc.arg(status_filter)::int)
  AND (sqlc.arg(kind)::text = ''
    OR (sqlc.arg(kind)::text = 'participants' AND participant.status = 2)
    OR (sqlc.arg(kind)::text = 'applications' AND NOT participant.invited AND participant.status IN (1, 3))
    OR (sqlc.arg(kind)::text = 'invitations' AND participant.invited AND participant.status = 1))
  AND (sqlc.arg(search)::text = ''
    OR person.email ILIKE '%' || sqlc.arg(search)::text || '%'
    OR (NOT (participant.invited AND participant.status = 1)
        AND (person.first_name || ' ' || person.last_name) ILIKE '%' || sqlc.arg(search)::text || '%')
    OR COALESCE(participant.pseudonym, '') ILIKE '%' || sqlc.arg(search)::text || '%')
  AND event_answers_match_all(listed.doc, sqlc.arg(filters)::jsonb)
ORDER BY CASE WHEN sqlc.arg(sort_dir)::text = 'asc' THEN event_list_sort_value(listed.doc, sqlc.arg(sort_key)::text) END ASC NULLS LAST,
         CASE WHEN sqlc.arg(sort_dir)::text = 'desc' THEN event_list_sort_value(listed.doc, sqlc.arg(sort_key)::text) END DESC NULLS LAST,
         participant.created_at DESC, participant.user_id DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountEventParticipantsTable :one
-- Mirrors ListEventParticipantsTable's filters.
SELECT count(*)
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
         LEFT JOIN event_teams team ON team.id = participant.team_id
         LEFT JOIN event_teams invited_team ON invited_team.id = participant.invited_team_id
         LEFT JOIN event_participant_presence presence ON presence.event_id = participant.event_id AND presence.user_id = participant.user_id
         CROSS JOIN LATERAL (SELECT event_user_last_lab_at(participant.event_id, participant.user_id) AS at) lab
         CROSS JOIN LATERAL (SELECT CASE
                                        WHEN sqlc.arg(with_answers)::boolean
                                            THEN COALESCE(event_registration_answers(participant.event_id, participant.user_id), '{}'::jsonb)
                                        ELSE '{}'::jsonb END
                                    || jsonb_build_object(
                                        -- A pending invitation is known by the address the organizer typed only:
                                        -- the profile name of an invited account is not theirs to search or sort by.
                                        '@name', CASE WHEN participant.invited AND participant.status = 1 THEN person.email
                                                      ELSE COALESCE(NULLIF(btrim(person.first_name || ' ' || person.last_name), ''),
                                                                    NULLIF(event_participant_public_name(participant.event_id, participant.user_id), ''),
                                                                    person.email) END,
                                        '@email', person.email,
                                        '@pseudonym', participant.pseudonym,
                                        '@status', participant.status::text,
                                        '@invitation', CASE WHEN participant.invitation_sent_at IS NULL THEN 'notSent' ELSE 'sent' END,
                                        '@team', CASE
                                                     WHEN participant.invited AND participant.status = 1
                                                         THEN COALESCE(participant.invited_team_id::text, 'none')
                                                     ELSE COALESCE(participant.team_id::text, 'none') END,
                                        '@teamName', CASE
                                                         WHEN participant.invited AND participant.status = 1 THEN invited_team.name
                                                         ELSE team.name END,
                                        '@missing', participant.fields_missing > 0,
                                        '@date', participant.created_at,
                                        '@lastSeen', presence.last_seen_at,
                                        '@lastLab', lab.at) AS doc) listed
WHERE participant.event_id = sqlc.arg(event_id)::uuid
  AND (sqlc.arg(status_filter)::int < 0 OR participant.status = sqlc.arg(status_filter)::int)
  AND (sqlc.arg(kind)::text = ''
    OR (sqlc.arg(kind)::text = 'participants' AND participant.status = 2)
    OR (sqlc.arg(kind)::text = 'applications' AND NOT participant.invited AND participant.status IN (1, 3))
    OR (sqlc.arg(kind)::text = 'invitations' AND participant.invited AND participant.status = 1))
  AND (sqlc.arg(search)::text = ''
    OR person.email ILIKE '%' || sqlc.arg(search)::text || '%'
    OR (NOT (participant.invited AND participant.status = 1)
        AND (person.first_name || ' ' || person.last_name) ILIKE '%' || sqlc.arg(search)::text || '%')
    OR COALESCE(participant.pseudonym, '') ILIKE '%' || sqlc.arg(search)::text || '%')
  AND event_answers_match_all(listed.doc, sqlc.arg(filters)::jsonb);
