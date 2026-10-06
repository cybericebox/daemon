-- name: ListEventParticipantRegistrationAnswers :many
-- Every participant of the event with the latest registration answers (NULL
-- when none): the input of the «missing required fields» recount.
SELECT participant.user_id, event_registration_answers(participant.event_id, participant.user_id)::jsonb AS answers
FROM event_participants participant
WHERE participant.event_id = sqlc.arg(event_id);

-- name: SetEventParticipantsFieldsMissing :exec
UPDATE event_participants participant
SET fields_missing = counted.missing
FROM (SELECT unnest(sqlc.arg(user_ids)::uuid[]) AS user_id, unnest(sqlc.arg(missing)::int[]) AS missing) counted
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.user_id = counted.user_id
  AND participant.fields_missing <> counted.missing;

-- name: GetEventParticipantFieldsMissing :one
SELECT fields_missing FROM event_participants
WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id);

-- name: CountEventRegistrationAnswers :one
SELECT count(DISTINCT answer.user_id)
FROM event_form_answers answer
         JOIN event_form_versions version ON version.id = answer.form_version_id
         JOIN event_forms form ON form.id = version.form_id
WHERE answer.event_id = sqlc.arg(event_id) AND form.purpose = 'registration';

-- name: GetLatestEventRegistrationAnswerRow :one
-- The participant's newest registration answer row (its version id keeps the
-- staff-only values next to the answers they belong to).
SELECT answer.form_version_id, answer.answers
FROM event_form_answers answer
         JOIN event_form_versions version ON version.id = answer.form_version_id
         JOIN event_forms form ON form.id = version.form_id
WHERE answer.event_id = sqlc.arg(event_id)
  AND answer.user_id = sqlc.arg(user_id)
  AND form.purpose = 'registration'
ORDER BY answer.submitted_at DESC
LIMIT 1;

-- name: UpdateEventFormAnswerValues :execrows
-- Staff-only edits change the values only: submitted_at stays the moment the
-- participant answered.
UPDATE event_form_answers
SET answers = sqlc.arg(answers)
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND form_version_id = sqlc.arg(form_version_id);

-- name: ListEventTeamExtraFields :many
SELECT id, extra_fields FROM event_teams WHERE event_id = sqlc.arg(event_id);

-- name: SetEventTeamsFieldsMissing :exec
UPDATE event_teams team
SET fields_missing = counted.missing
FROM (SELECT unnest(sqlc.arg(team_ids)::uuid[]) AS team_id, unnest(sqlc.arg(missing)::int[]) AS missing) counted
WHERE team.event_id = sqlc.arg(event_id)
  AND team.id = counted.team_id
  AND team.fields_missing <> counted.missing;

-- name: GetEventTeamFieldsMissing :one
SELECT fields_missing FROM event_teams
WHERE event_id = sqlc.arg(event_id) AND id = sqlc.arg(team_id);

-- name: InsertEventStaffFieldChange :exec
INSERT INTO event_staff_field_changes (id, event_id, scope, subject_id, actor_id, field_keys, changed_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(scope), sqlc.arg(subject_id), sqlc.arg(actor_id), sqlc.arg(field_keys)::text[], sqlc.arg(changed_at));

-- name: GetLastEventStaffFieldChange :one
SELECT change.field_keys, change.changed_at, change.actor_id,
       COALESCE(NULLIF(btrim(actor.first_name || ' ' || actor.last_name), ''), actor.email, '')::text AS actor_name
FROM event_staff_field_changes change
         LEFT JOIN users actor ON actor.id = change.actor_id
WHERE change.event_id = sqlc.arg(event_id)
  AND change.scope = sqlc.arg(scope)
  AND change.subject_id = sqlc.arg(subject_id)
ORDER BY change.changed_at DESC
LIMIT 1;
