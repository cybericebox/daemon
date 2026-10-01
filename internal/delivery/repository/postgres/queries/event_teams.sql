-- name: CreateEventTeam :one
INSERT INTO event_teams (id, event_id, name, join_code, captain_id, hidden, member_count, created_at, updated_at,
                         individual, admitted_manually, admission_locked, formed_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(name), sqlc.arg(join_code), sqlc.arg(captain_id),
        sqlc.arg(hidden), sqlc.arg(member_count), sqlc.arg(created_at), sqlc.arg(updated_at),
        sqlc.arg(individual), sqlc.arg(admitted_manually), sqlc.arg(admission_locked), sqlc.narg(formed_at))
RETURNING *;

-- name: GetEventTeamByID :one
-- The hidden moderators team is invisible to every participant and team
-- management path; the stand engine reads it through its own queries.
SELECT *
FROM event_teams
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND NOT moderators;

-- name: GetEventTeamByJoinCode :one
SELECT *
FROM event_teams
WHERE event_id = sqlc.arg(event_id)
  AND join_code = sqlc.arg(join_code)
  AND NOT moderators;

-- name: GetEventTeamForParticipant :one
SELECT t.*
FROM event_teams t
JOIN event_participants p ON p.team_id = t.id
WHERE p.event_id = sqlc.arg(event_id)
  AND p.user_id = sqlc.arg(user_id);

-- name: CountEventTeams :one
SELECT count(*)
FROM event_teams
WHERE event_id = sqlc.arg(event_id)
  AND NOT moderators;

-- name: CountApprovedEventTeams :one
SELECT count(DISTINCT team_id)
FROM event_participants participant
JOIN event_teams team ON team.id = participant.team_id
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.status = 2
  AND event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually, team.admission_locked, team.member_count);

-- name: CountEventTeamsFiltered :one
-- Mirrors ListEventTeams' search, admission and field filters for the management list.
SELECT count(*)
FROM event_teams
WHERE event_id = sqlc.arg(event_id)
  AND NOT moderators
  AND (sqlc.arg(search)::text = '' OR name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(admission_filter)::int < 0
    OR event_team_admitted(event_id, individual, admitted_manually, admission_locked, member_count) = (sqlc.arg(admission_filter)::int = 1))
  AND (jsonb_array_length(sqlc.arg(field_filters)::jsonb) = 0
    OR event_answers_match_all(extra_fields, sqlc.arg(field_filters)::jsonb));

-- name: ListEventTeams :many
-- search '' means no text filter; admission_filter < 0 means any admission,
-- 1 only admitted teams and 0 only teams that are not admitted; field_filters
-- is a JSON array of answer filters on extra_fields (event_answer_matches).
SELECT sqlc.embed(event_teams),
       event_team_admitted(event_id, individual, admitted_manually, admission_locked, member_count)::boolean AS admitted
FROM event_teams
WHERE event_id = sqlc.arg(event_id)
  AND NOT moderators
  AND (sqlc.arg(search)::text = '' OR name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(admission_filter)::int < 0
    OR event_team_admitted(event_id, individual, admitted_manually, admission_locked, member_count) = (sqlc.arg(admission_filter)::int = 1))
  AND (jsonb_array_length(sqlc.arg(field_filters)::jsonb) = 0
    OR event_answers_match_all(extra_fields, sqlc.arg(field_filters)::jsonb))
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_val);

-- name: DeleteEventTeam :execrows
-- participant.team_id is ON DELETE SET NULL, so deleting a disbanded team
-- atomically detaches every remaining member at the database level.
DELETE FROM event_teams
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: UpdateEventTeam :execrows
UPDATE event_teams
SET name         = sqlc.arg(name),
    join_code    = sqlc.arg(join_code),
    join_code_expires_at = sqlc.arg(join_code_expires_at),
    captain_id   = sqlc.arg(captain_id),
    hidden       = sqlc.arg(hidden),
    member_count = sqlc.arg(member_count),
    admitted_manually = sqlc.arg(admitted_manually),
    admission_locked  = sqlc.arg(admission_locked),
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND updated_at = sqlc.arg(expected_updated_at);

-- name: TryAddEventTeamMember :execrows
-- max_team_size is supplied by the event config inside the same transaction.
UPDATE event_teams
SET member_count = member_count + 1,
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND member_count < sqlc.arg(max_team_size);

-- name: TryRemoveEventTeamMember :execrows
UPDATE event_teams
SET member_count = member_count - 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND member_count > 1;

-- name: GetEventTeamAdmitted :one
SELECT event_team_admitted(event_id, individual, admitted_manually, admission_locked, member_count)::boolean AS admitted
FROM event_teams
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: GetEventTeamVisible :one
SELECT event_team_visible(hidden, moderators, event_id, individual, admitted_manually, admission_locked, member_count)::boolean AS visible
FROM event_teams
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: SetEventTeamHidden :execrows
-- Presentation-only toggle of the results visibility. The moderators team is
-- always hidden and never toggles. expected_updated_at guards a concurrent edit.
UPDATE event_teams
SET hidden     = sqlc.arg(hidden),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND NOT moderators
  AND hidden <> sqlc.arg(hidden);

-- name: GetEventMinTeamSize :one
SELECT COALESCE(event_min_team_size(sqlc.arg(event_id)::uuid), 1)::integer AS min_team_size;

-- name: GetEventTeamByName :one
-- Team names are unique per event; a batch re-run finds its own team.
SELECT *
FROM event_teams
WHERE event_id = sqlc.arg(event_id)
  AND name = sqlc.arg(name)
  AND NOT moderators;

-- name: ListEventTeamsTable :many
-- Manage table (offset mode): every column can filter and sort. A team is
-- matched as a document of its standard columns ("@name", "@captain",
-- "@members", "@status", …) over its extra field answers.
SELECT sqlc.embed(event_teams),
       event_team_admitted(event_teams.event_id, event_teams.individual, event_teams.admitted_manually,
                           event_teams.admission_locked, event_teams.member_count)::boolean AS admitted
FROM event_teams
         CROSS JOIN LATERAL (SELECT event_teams.extra_fields || jsonb_build_object(
                                        '@name', event_teams.name,
                                        '@captain', (SELECT btrim(captain.first_name || ' ' || captain.last_name || ' ' || captain.email)
                                                     FROM users captain
                                                     WHERE captain.id = event_teams.captain_id),
                                        '@captainPending', EXISTS (SELECT 1
                                                                   FROM event_participants invitee
                                                                   WHERE invitee.event_id = event_teams.event_id
                                                                     AND invitee.user_id = event_teams.captain_id
                                                                     AND invitee.invited
                                                                     AND invitee.status = 1
                                                                     AND invitee.invited_team_id = event_teams.id),
                                        '@members', event_teams.member_count,
                                        '@status', CASE
                                                       WHEN event_teams.admitted_manually THEN 'manual'
                                                       WHEN event_team_admitted(event_teams.event_id, event_teams.individual, event_teams.admitted_manually,
                                                                                event_teams.admission_locked, event_teams.member_count) THEN 'admitted'
                                                       ELSE 'notAdmitted' END,
                                        '@created', event_teams.created_at,
                                        '@missing', event_teams.fields_missing > 0,
                                        '@pending', EXISTS (SELECT 1
                                                            FROM event_participants invitee
                                                            WHERE invitee.event_id = event_teams.event_id
                                                              AND invitee.invited
                                                              AND invitee.status = 1
                                                              AND invitee.invited_team_id = event_teams.id)) AS doc) listed
WHERE event_teams.event_id = sqlc.arg(event_id)
  AND NOT event_teams.moderators
  AND (sqlc.arg(search)::text = '' OR event_teams.name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND event_answers_match_all(listed.doc, sqlc.arg(filters)::jsonb)
ORDER BY CASE WHEN sqlc.arg(sort_dir)::text = 'asc' THEN event_list_sort_value(listed.doc, sqlc.arg(sort_key)::text) END ASC NULLS LAST,
         CASE WHEN sqlc.arg(sort_dir)::text = 'desc' THEN event_list_sort_value(listed.doc, sqlc.arg(sort_key)::text) END DESC NULLS LAST,
         event_teams.created_at DESC, event_teams.id DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountEventTeamsTable :one
-- Mirrors ListEventTeamsTable's filters.
SELECT count(*)
FROM event_teams
         CROSS JOIN LATERAL (SELECT event_teams.extra_fields || jsonb_build_object(
                                        '@name', event_teams.name,
                                        '@captain', (SELECT btrim(captain.first_name || ' ' || captain.last_name || ' ' || captain.email)
                                                     FROM users captain
                                                     WHERE captain.id = event_teams.captain_id),
                                        '@captainPending', EXISTS (SELECT 1
                                                                   FROM event_participants invitee
                                                                   WHERE invitee.event_id = event_teams.event_id
                                                                     AND invitee.user_id = event_teams.captain_id
                                                                     AND invitee.invited
                                                                     AND invitee.status = 1
                                                                     AND invitee.invited_team_id = event_teams.id),
                                        '@members', event_teams.member_count,
                                        '@status', CASE
                                                       WHEN event_teams.admitted_manually THEN 'manual'
                                                       WHEN event_team_admitted(event_teams.event_id, event_teams.individual, event_teams.admitted_manually,
                                                                                event_teams.admission_locked, event_teams.member_count) THEN 'admitted'
                                                       ELSE 'notAdmitted' END,
                                        '@created', event_teams.created_at,
                                        '@missing', event_teams.fields_missing > 0,
                                        '@pending', EXISTS (SELECT 1
                                                            FROM event_participants invitee
                                                            WHERE invitee.event_id = event_teams.event_id
                                                              AND invitee.invited
                                                              AND invitee.status = 1
                                                              AND invitee.invited_team_id = event_teams.id)) AS doc) listed
WHERE event_teams.event_id = sqlc.arg(event_id)
  AND NOT event_teams.moderators
  AND (sqlc.arg(search)::text = '' OR event_teams.name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND event_answers_match_all(listed.doc, sqlc.arg(filters)::jsonb);

-- name: GetEventTeamFormed :one
-- The one formation rule (event_team_formed): an explicit formation, or the start
-- of an event without late join.
SELECT event_team_formed(event_id, formed_at)::boolean AS formed
FROM event_teams
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: FormEventTeam :execrows
-- Closes the roster for good. formed_by NULL means no person did it. Already
-- formed teams are left alone, so a repeat is a no-op.
UPDATE event_teams
SET formed_at  = sqlc.arg(formed_at),
    formed_by  = sqlc.narg(formed_by),
    updated_at = sqlc.arg(formed_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND formed_at IS NULL;
