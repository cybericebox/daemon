-- name: ListEditableImportTargets :many
-- Apply the exact write policy and archive freeze BEFORE keyset pagination.
SELECT e.id, COALESCE(v.name, e.name)::text AS name, e.scope, e.owner_event_id,
       COALESCE(NULLIF(ev.internal_name, ''), ev.name, '')::text AS owner_event_name,
       COALESCE(ev.tag, '')::text AS owner_event_tag,
       (e.draft_version_id IS NOT NULL)::boolean AS has_draft
FROM exercises e
LEFT JOIN exercise_versions v ON v.id = COALESCE(e.draft_version_id, e.published_version_id)
LEFT JOIN events ev ON ev.id = e.owner_event_id
WHERE e.archived_at IS NULL
  AND (sqlc.arg(writer_all)::boolean OR (e.scope = 1 AND EXISTS (
      SELECT 1 FROM event_managers member
      WHERE member.event_id = e.owner_event_id AND member.user_id = sqlc.arg(viewer_id)
        AND member.role IN (0, 1))))
  AND (sqlc.arg(search)::text = '' OR COALESCE(v.name, e.name) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(cursor_id)::uuid IS NULL OR e.id < sqlc.narg(cursor_id)::uuid)
ORDER BY e.id DESC
LIMIT sqlc.arg(limit_val);

-- name: CountEditableImportTargets :one
SELECT count(*) FROM exercises e
LEFT JOIN exercise_versions v ON v.id = COALESCE(e.draft_version_id, e.published_version_id)
WHERE e.archived_at IS NULL
  AND (sqlc.arg(writer_all)::boolean OR (e.scope = 1 AND EXISTS (
      SELECT 1 FROM event_managers member
      WHERE member.event_id = e.owner_event_id AND member.user_id = sqlc.arg(viewer_id)
        AND member.role IN (0, 1))))
  AND (sqlc.arg(search)::text = '' OR COALESCE(v.name, e.name) ILIKE '%' || sqlc.arg(search)::text || '%');

-- name: GetImportTargetContext :one
-- Caller has already checked ActionWrite and the archive freeze for this ID.
SELECT COALESCE(NULLIF(ev.internal_name, ''), ev.name, '')::text AS owner_event_name,
       COALESCE(ev.tag, '')::text AS owner_event_tag
FROM exercises e LEFT JOIN events ev ON ev.id = e.owner_event_id
WHERE e.id = $1;

-- name: ListExerciseActiveAttachments :many
-- The active attachments (status 0: neither superseded nor detached) of an exercise in events, with what each runs
-- now: its pinned version and how many team laboratories of it are running. Read by the import warning.
SELECT ee.id AS event_exercise_id,
       ee.event_id,
       COALESCE(NULLIF(ev.internal_name, ''), ev.name)::text AS event_name,
       (ev.archive_at IS NOT NULL AND ev.archive_at <= sqlc.arg(now)::timestamptz)::boolean AS archived,
       ee.exercise_version_id,
       ee.variant_mode,
       ee.fixed_variant_index,
       (SELECT count(*) FROM event_team_labs lab
        WHERE lab.event_exercise_id = ee.id AND lab.desired_state = 'Running')::int AS running_labs
FROM event_exercises ee
JOIN events ev ON ev.id = ee.event_id
WHERE ee.exercise_id = sqlc.arg(exercise_id)
  AND ee.status = 0
ORDER BY lower(COALESCE(NULLIF(ev.internal_name, ''), ev.name)), ee.id;

-- name: ListExerciseChallengeAnswerCounts :many
-- Recorded answers per board challenge of the active attachments of an exercise: every attempt, and the teams that
-- solved it (a correct attempt that is not practice).
SELECT ch.event_exercise_id,
       ch.task_id,
       count(a.id)::int AS attempts,
       count(DISTINCT tc.id) FILTER (WHERE a.correct AND NOT a.practice)::int AS solves
FROM event_challenges ch
JOIN event_exercises ee ON ee.id = ch.event_exercise_id
LEFT JOIN team_challenges tc ON tc.event_challenge_id = ch.id
LEFT JOIN challenge_attempts a ON a.team_challenge_id = tc.id
WHERE ee.exercise_id = sqlc.arg(exercise_id)
  AND ee.status = 0
GROUP BY ch.event_exercise_id, ch.task_id;

-- name: CountLeasedExerciseTestDeploys :one
-- The test labs of an exercise that are leased now, any author.
SELECT count(*)::int
FROM exercise_test_deployments d
JOIN exercise_versions v ON v.id = d.version_id
WHERE v.exercise_id = sqlc.arg(exercise_id)
  AND d.expires_at > sqlc.arg(now)::timestamptz;
