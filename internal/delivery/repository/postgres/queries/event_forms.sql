-- name: GetLatestEventFormVersion :one
SELECT version.*
FROM event_form_versions version
JOIN event_forms form ON form.id = version.form_id
WHERE version.event_id = sqlc.arg(event_id)
  AND form.purpose = 'registration'
ORDER BY version.created_at DESC, version.id DESC
LIMIT 1;

-- name: GetEventFormVersionByID :one
SELECT *
FROM event_form_versions
WHERE id = sqlc.arg(id) AND event_id = sqlc.arg(event_id);

-- name: GetLatestEventFormVersionByFormID :one
SELECT *
FROM event_form_versions
WHERE form_id = sqlc.arg(form_id)
ORDER BY version DESC, id DESC
LIMIT 1;

-- name: CreateInitialEventForm :one
WITH created_form AS (
    INSERT INTO event_forms (id, event_id, title, purpose, enabled, required, created_at, updated_at)
    VALUES (sqlc.arg(form_id), sqlc.arg(event_id), sqlc.arg(title), sqlc.arg(purpose),
            sqlc.arg(enabled), sqlc.arg(required), sqlc.arg(created_at), sqlc.arg(created_at))
)
INSERT INTO event_form_versions (id, form_id, event_id, version, enabled, required, document, created_at, require_existing, block_submissions)
VALUES (sqlc.arg(id), sqlc.arg(form_id), sqlc.arg(event_id), sqlc.arg(version), sqlc.arg(enabled), sqlc.arg(required), sqlc.arg(document), sqlc.arg(created_at),
        sqlc.arg(require_existing), sqlc.arg(block_submissions))
RETURNING *;

-- name: ListEventForms :many
SELECT form.*, version.id AS current_version_id, version.version AS current_version,
       version.document AS current_document, version.created_at AS version_created_at
FROM event_forms form
JOIN LATERAL (
    SELECT *
    FROM event_form_versions
    WHERE form_id = form.id
    ORDER BY version DESC, id DESC
    LIMIT 1
) version ON true
WHERE form.event_id = sqlc.arg(event_id)
  AND form.purpose = 'other'
ORDER BY form.created_at ASC, form.id ASC;

-- name: GetEventForm :one
SELECT form.*, version.id AS current_version_id, version.version AS current_version,
       version.document AS current_document, version.created_at AS version_created_at
FROM event_forms form
JOIN LATERAL (
    SELECT *
    FROM event_form_versions
    WHERE form_id = form.id
    ORDER BY version DESC, id DESC
    LIMIT 1
) version ON true
WHERE form.event_id = sqlc.arg(event_id)
  AND form.purpose = 'other'
  AND form.id = sqlc.arg(form_id);

-- name: CreateEventFormVersion :one
INSERT INTO event_form_versions (id, form_id, event_id, version, enabled, required, document, created_at, require_existing, block_submissions)
VALUES (sqlc.arg(id), sqlc.arg(form_id), sqlc.arg(event_id), sqlc.arg(version), sqlc.arg(enabled), sqlc.arg(required), sqlc.arg(document), sqlc.arg(created_at),
        sqlc.arg(require_existing), sqlc.arg(block_submissions))
RETURNING *;

-- name: PublishEventFormVersion :one
WITH created_version AS (
    INSERT INTO event_form_versions (id, form_id, event_id, version, enabled, required, document, created_at)
    VALUES (sqlc.arg(id), sqlc.arg(form_id), sqlc.arg(event_id), sqlc.arg(version), sqlc.arg(enabled), sqlc.arg(required), sqlc.arg(document), sqlc.arg(created_at))
    RETURNING *
), updated_form AS (
    UPDATE event_forms
    SET title = sqlc.arg(title),
        enabled = sqlc.arg(enabled),
        required = sqlc.arg(required),
        updated_at = sqlc.arg(updated_at)
    WHERE id = sqlc.arg(form_id) AND event_id = sqlc.arg(event_id)
    RETURNING id
)
SELECT created_version.*
FROM created_version
JOIN updated_form ON true;

-- name: UpdateEventFormSettings :execrows
UPDATE event_forms
SET enabled = sqlc.arg(enabled),
    required = sqlc.arg(required),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(form_id);

-- name: UpdateEventFormTitle :execrows
UPDATE event_forms
SET title = sqlc.arg(title),
    updated_at = sqlc.arg(updated_at)
WHERE event_id = sqlc.arg(event_id)
  AND id = sqlc.arg(form_id);

-- name: GetEventFormAnswer :one
SELECT answer.*
FROM event_form_answers answer
WHERE answer.event_id = sqlc.arg(event_id)
  AND answer.user_id = sqlc.arg(user_id)
  AND answer.form_version_id = sqlc.arg(form_version_id)
LIMIT 1;

-- name: UpsertEventFormAnswer :one
INSERT INTO event_form_answers (event_id, user_id, form_version_id, answers, submitted_at)
VALUES (sqlc.arg(event_id), sqlc.arg(user_id), sqlc.arg(form_version_id), sqlc.arg(answers), sqlc.arg(submitted_at))
ON CONFLICT (form_version_id, user_id) DO UPDATE
SET answers = EXCLUDED.answers,
    submitted_at = EXCLUDED.submitted_at
RETURNING *;

-- name: ListEventFormAnswers :many
SELECT answer.*, version.version, version.form_id, version.document,
       TRIM(CONCAT_WS(' ', person.first_name, person.last_name)) AS name,
       person.email
FROM event_form_answers answer
JOIN event_form_versions version ON version.id = answer.form_version_id
JOIN users person ON person.id = answer.user_id
WHERE answer.event_id = sqlc.arg(event_id)
  AND version.form_id = sqlc.arg(form_id)
ORDER BY answer.submitted_at DESC, answer.user_id ASC;

-- name: CreateEventFormAssignment :one
INSERT INTO event_form_assignments (
    id, event_id, form_id, trigger, audience, include_future_participants,
    presentation, dismissible, gates, at, enabled, created_at, updated_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(form_id), sqlc.arg(trigger), sqlc.arg(audience),
    sqlc.arg(include_future_participants), sqlc.arg(presentation), sqlc.arg(dismissible),
    sqlc.arg(gates), sqlc.arg(at), sqlc.arg(enabled), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetEventFormDelivery :one
SELECT delivery.*
FROM event_form_deliveries delivery
JOIN event_form_versions version ON version.id = delivery.form_version_id
WHERE version.event_id = sqlc.arg(event_id)
  AND version.form_id = sqlc.arg(form_id)
  AND delivery.user_id = sqlc.arg(user_id)
ORDER BY delivery.created_at DESC
LIMIT 1;

-- name: ListEventFormDeliveries :many
SELECT delivery.*
FROM event_form_deliveries delivery
JOIN event_form_versions version ON version.id = delivery.form_version_id
WHERE version.event_id = sqlc.arg(event_id)
  AND version.form_id = sqlc.arg(form_id)
ORDER BY delivery.created_at ASC, delivery.user_id ASC;

-- name: ListPendingEventFormDeliveries :many
SELECT delivery.*, form.id AS form_id, form.title, form.enabled AS form_enabled,
       form.required AS form_required, version.version, version.document
FROM event_form_deliveries delivery
JOIN event_form_versions version ON version.id = delivery.form_version_id
JOIN event_forms form ON form.id = version.form_id
WHERE version.event_id = sqlc.arg(event_id)
  AND delivery.user_id = sqlc.arg(user_id)
  AND delivery.completed_at IS NULL
  AND form.enabled
ORDER BY delivery.created_at ASC, delivery.form_version_id ASC;

-- name: CompleteEventFormDelivery :execrows
UPDATE event_form_deliveries
SET completed_at = sqlc.arg(completed_at)
WHERE form_version_id = sqlc.arg(form_version_id)
  AND user_id = sqlc.arg(user_id)
  AND completed_at IS NULL;

-- name: CreateEventFormDelivery :execrows
INSERT INTO event_form_deliveries (
    form_version_id, user_id, assignment_id, presentation, dismissible, gates, created_at
)
VALUES (
    sqlc.arg(form_version_id), sqlc.arg(user_id), sqlc.arg(assignment_id),
    sqlc.arg(presentation), sqlc.arg(dismissible), sqlc.arg(gates), sqlc.arg(created_at)
)
ON CONFLICT (form_version_id, user_id) DO NOTHING;

-- name: ListEventFormAssignmentsByTrigger :many
SELECT assignment.*
FROM event_form_assignments assignment
JOIN event_forms form ON form.id = assignment.form_id
WHERE assignment.event_id = sqlc.arg(event_id)
  AND assignment.trigger = sqlc.arg(trigger)
  AND assignment.enabled
  AND form.enabled
ORDER BY assignment.created_at ASC, assignment.id ASC;

-- name: ListActiveFutureTimedEventFormAssignments :many
SELECT assignment.*
FROM event_form_assignments assignment
JOIN event_forms form ON form.id = assignment.form_id
WHERE assignment.event_id = sqlc.arg(event_id)
  AND assignment.trigger IN ('at_time', 'manual')
  AND assignment.enabled
  AND assignment.include_future_participants
  AND assignment.materialized_at IS NOT NULL
  AND form.enabled
ORDER BY assignment.created_at ASC, assignment.id ASC;

-- name: ListEventFormRecipientCandidates :many
SELECT participant.user_id, participant.team_id, participant.team_role,
       COALESCE(team.member_count, 0)::int AS team_member_count
FROM event_participants participant
LEFT JOIN event_teams team ON team.id = participant.team_id
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.status = 2;

-- name: ListDueTimedEventFormAssignmentsForUpdate :many
SELECT assignment.*
FROM event_form_assignments assignment
JOIN event_forms form ON form.id = assignment.form_id
JOIN events event ON event.id = assignment.event_id
WHERE (
        (assignment.trigger = 'at_time' AND assignment.at <= sqlc.arg(now_at))
        OR (
            assignment.trigger = 'event_finished'
            AND COALESCE(event.manual_finished_at, event.finish_at) <= sqlc.arg(now_at)
        )
    )
  AND assignment.enabled
  AND form.enabled
  AND assignment.materialized_at IS NULL
ORDER BY COALESCE(assignment.at, event.manual_finished_at, event.finish_at) ASC, assignment.id ASC
FOR UPDATE OF assignment SKIP LOCKED
LIMIT sqlc.arg(limit_val);

-- name: MarkEventFormAssignmentMaterialized :execrows
UPDATE event_form_assignments
SET materialized_at = sqlc.arg(materialized_at),
    updated_at = sqlc.arg(materialized_at)
WHERE id = sqlc.arg(id)
  AND materialized_at IS NULL;

-- name: HasIncompleteRequiredEventFormDelivery :one
SELECT EXISTS (
    SELECT 1
    FROM event_form_deliveries delivery
    JOIN event_form_versions version ON version.id = delivery.form_version_id
    JOIN event_forms form ON form.id = version.form_id
    WHERE version.event_id = sqlc.arg(event_id)
      AND delivery.user_id = sqlc.arg(user_id)
      AND delivery.completed_at IS NULL
      AND form.enabled
      AND form.required
      AND version.enabled
      AND delivery.gates ? sqlc.arg(capability)
);

-- name: ListLatestRegistrationAnswersForUsers :many
-- Latest registration-form answer per listed user (moderation list columns).
SELECT DISTINCT ON (answer.user_id) answer.user_id, answer.answers
FROM event_form_answers answer
         JOIN event_form_versions version ON version.id = answer.form_version_id
         JOIN event_forms form ON form.id = version.form_id
WHERE answer.event_id = sqlc.arg(event_id)
  AND form.purpose = 'registration'
  AND answer.user_id = ANY (sqlc.arg(user_ids)::uuid[])
ORDER BY answer.user_id, answer.submitted_at DESC;
