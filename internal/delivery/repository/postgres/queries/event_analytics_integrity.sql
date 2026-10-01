-- Event analytics, «Доброчесність» (docs/EVENT-ANALYTICS.md §6.6, docs/ANTI-CHEAT.md).
-- The raw facts the per-solve signals in eventAnalyticsModel read. No IP or
-- user agent is kept anywhere (§9.3). Hidden teams and the moderators team are
-- never counted (event_team_visible). Answers are cut to 200 characters.
-- 'epoch' stands for «no such moment».

-- name: ListEventIntegrityAttempts :many
SELECT a.id,
       a.event_team_id                                                              AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       a.team_challenge_id,
       tc.event_challenge_id                                                        AS challenge_id,
       COALESCE(c.snapshot ->> 'name', '')::text                                    AS challenge_name,
       a.user_id,
       left(a.answer, 200)::text                                                    AS answer,
       a.effective_correct                                                          AS correct,
       a.received_at
FROM effective_challenge_attempts a
JOIN event_teams t ON t.id = a.event_team_id
    AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
JOIN team_challenges tc ON tc.id = a.team_challenge_id
LEFT JOIN event_challenges c ON c.id = tc.event_challenge_id
WHERE a.event_id = sqlc.arg(event_id)
ORDER BY a.received_at, a.id;

-- name: ListEventIntegrityRejections :many
-- Submissions the platform refused (rate limit, after the finish, ...), which
-- never became attempts.
SELECT o.team_id::uuid                                                              AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       o.subject_id::uuid                                                           AS challenge_id,
       COALESCE(c.snapshot ->> 'name', '')::text                                    AS challenge_name,
       COALESCE(o.data ->> 'reason', '')::text                                      AS reason,
       o.at
FROM event_activity o
JOIN event_teams t ON t.id = o.team_id
    AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
LEFT JOIN event_challenges c ON c.id = o.subject_id
WHERE o.event_id = sqlc.arg(event_id)
  AND o.kind = 'attempt_rejected'
  AND o.subject_id IS NOT NULL
ORDER BY o.at, o.id;

-- name: ListEventIntegritySolves :many
-- One row per solve with the access evidence of the solving team: its first
-- open of the task, first attachment download, first hint unlock and first
-- VPN session. static_flag: 1 = several teams hold the same expected flag,
-- 0 = every team has its own, -1 = unknown (fewer than two teams). The
-- expected flag itself never leaves SQL.
WITH flags AS (
    SELECT f.id,
           count(*) OVER (PARTITION BY f.event_challenge_id, f.expected_flag) AS same_flag,
           count(*) OVER (PARTITION BY f.event_challenge_id)                  AS materialized
    FROM team_challenges f
    WHERE f.event_id = sqlc.arg(event_id)
)
SELECT tc.id                                                                        AS team_challenge_id,
       tc.event_team_id                                                             AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       tc.event_challenge_id                                                        AS challenge_id,
       COALESCE(c.snapshot ->> 'name', '')::text                                    AS challenge_name,
       ee.exercise_id                                                               AS exercise_id,
       c.task_id                                                                    AS task_id,
       COALESCE(c.snapshot ->> 'difficulty', '')::text                              AS level,
       CASE WHEN jsonb_typeof(c.snapshot -> 'attachments') = 'array'
                THEN jsonb_array_length(c.snapshot -> 'attachments') ELSE 0 END::int AS attachment_count,
       s.solved_at,
       EXISTS (SELECT 1
               FROM lab_bindings lb
               WHERE lb.event_team_id = tc.event_team_id
                 AND lb.event_challenge_id = tc.event_challenge_id)::boolean            AS has_lab,
       (CASE WHEN fl.materialized < 2 THEN -1 WHEN fl.same_flag > 1 THEN 1 ELSE 0 END)::int AS static_flag,
       COALESCE((SELECT min(o.at)
                 FROM event_activity o
                 WHERE o.event_id = tc.event_id AND o.kind = 'task_opened'
                   AND o.subject_id = tc.event_challenge_id AND o.team_id = tc.event_team_id),
                'epoch'::timestamptz)::timestamptz                                  AS first_open_at,
       COALESCE((SELECT min(o.at)
                 FROM event_activity o
                 WHERE o.event_id = tc.event_id AND o.kind = 'attachment_downloaded'
                   AND o.subject_id = tc.event_challenge_id AND o.team_id = tc.event_team_id),
                'epoch'::timestamptz)::timestamptz                                  AS first_download_at,
       COALESCE((SELECT min(h.unlocked_at)
                 FROM team_challenge_hint_unlocks h
                 WHERE h.team_challenge_id = tc.id),
                'epoch'::timestamptz)::timestamptz                                  AS first_hint_at,
       COALESCE((SELECT min(v.started_at)
                 FROM event_vpn_sessions v
                 WHERE v.event_id = tc.event_id AND v.team_id = tc.event_team_id),
                'epoch'::timestamptz)::timestamptz                                  AS first_vpn_at
FROM team_challenge_solves s
JOIN team_challenges tc ON tc.id = s.team_challenge_id
JOIN flags fl ON fl.id = tc.id
JOIN event_teams t ON t.id = tc.event_team_id
    AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
JOIN event_challenges c ON c.id = tc.event_challenge_id
JOIN event_exercises ee ON ee.id = c.event_exercise_id
WHERE tc.event_id = sqlc.arg(event_id)
ORDER BY s.solved_at, tc.id;

-- name: ListEventIntegrityCrossFlags :many
-- Submissions equal to the expected flag of ANOTHER team's task (any task of the
-- event), where the value is not one of the submitting team's own flags. A
-- value held by more than one team task (a flag shared by all) is skipped, so
-- only per-team flags can raise it. The flags themselves never leave SQL.
SELECT a.team_challenge_id                                                          AS team_challenge_id,
       a.event_team_id                                                              AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       tc.event_challenge_id                                                        AS challenge_id,
       COALESCE(c.snapshot ->> 'name', '')::text                                    AS challenge_name,
       ee.exercise_id                                                               AS exercise_id,
       c.task_id                                                                    AS task_id,
       COALESCE(c.snapshot ->> 'difficulty', '')::text                              AS level,
       a.received_at                                                                AS at,
       owner.event_team_id                                                          AS owner_team_id,
       event_team_public_name(ot.individual, ot.event_id, ot.captain_id, ot.name)::text AS owner_team_name,
       owner.event_challenge_id                                                     AS owner_challenge_id,
       COALESCE(oc.snapshot ->> 'name', '')::text                                   AS owner_challenge_name
FROM effective_challenge_attempts a
JOIN event_teams t ON t.id = a.event_team_id
    AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
JOIN team_challenges tc ON tc.id = a.team_challenge_id
JOIN event_challenges c ON c.id = tc.event_challenge_id
JOIN event_exercises ee ON ee.id = c.event_exercise_id
JOIN team_challenges owner ON owner.event_id = a.event_id
    AND owner.expected_flag = btrim(a.answer)
    AND owner.event_team_id <> a.event_team_id
JOIN event_teams ot ON ot.id = owner.event_team_id
JOIN event_challenges oc ON oc.id = owner.event_challenge_id
WHERE a.event_id = sqlc.arg(event_id)
  AND (SELECT count(*)
       FROM team_challenges u
       WHERE u.event_id = a.event_id AND u.expected_flag = owner.expected_flag) = 1
  AND NOT EXISTS (SELECT 1
                  FROM team_challenges own
                  WHERE own.event_id = a.event_id
                    AND own.event_team_id = a.event_team_id
                    AND own.expected_flag = btrim(a.answer))
ORDER BY a.received_at, a.id;

-- name: GetEventIntegrityCollectorStart :one
-- The first task open the event recorded: solves before it cannot be judged
-- by access (the collector did not exist yet). 'epoch' = none recorded.
SELECT COALESCE(min(o.at), 'epoch'::timestamptz)::timestamptz AS first_open_at
FROM event_activity o
WHERE o.event_id = sqlc.arg(event_id)
  AND o.kind = 'task_opened';

-- name: ListSolveIntegrityReviews :many
SELECT r.team_challenge_id,
       r.note,
       r.reviewed_by,
       concat_ws(' ', u.first_name, u.last_name)::text AS reviewed_by_name,
       r.reviewed_at
FROM solve_integrity_reviews r
LEFT JOIN users u ON u.id = r.reviewed_by
WHERE r.event_id = sqlc.arg(event_id);

-- name: UpsertSolveIntegrityReview :one
-- Only a team task of the event may be reviewed: no row when it is not.
INSERT INTO solve_integrity_reviews (team_challenge_id, event_id, note, reviewed_by, reviewed_at)
SELECT tc.id, tc.event_id, sqlc.arg(note), sqlc.arg(reviewed_by), sqlc.arg(reviewed_at)
FROM team_challenges tc
WHERE tc.id = sqlc.arg(team_challenge_id)
  AND tc.event_id = sqlc.arg(event_id)
ON CONFLICT (team_challenge_id) DO UPDATE
    SET note        = EXCLUDED.note,
        reviewed_by = EXCLUDED.reviewed_by,
        reviewed_at = EXCLUDED.reviewed_at
RETURNING team_challenge_id;

-- name: DeleteSolveIntegrityReview :execrows
DELETE
FROM solve_integrity_reviews
WHERE team_challenge_id = sqlc.arg(team_challenge_id)
  AND event_id = sqlc.arg(event_id);

-- name: GetIntegrityTaskOfTeamChallenge :one
-- The catalog task behind a team task of the event.
SELECT ee.exercise_id AS exercise_id, c.task_id AS task_id
FROM team_challenges tc
         JOIN event_challenges c ON c.id = tc.event_challenge_id
         JOIN event_exercises ee ON ee.id = c.event_exercise_id
WHERE tc.id = sqlc.arg(team_challenge_id)
  AND tc.event_id = sqlc.arg(event_id);

-- name: ListIntegrityDismissals :many
-- The dismissals that apply to the event: its own and those of the catalog
-- exercises it uses.
SELECT d.id,
       d.scope,
       d.exercise_id,
       d.task_id,
       d.kind,
       d.key,
       d.note,
       concat_ws(' ', u.first_name, u.last_name)::text AS created_by_name,
       d.created_at,
       COALESCE((SELECT c.snapshot ->> 'name'
                 FROM event_challenges c
                          JOIN event_exercises ee ON ee.id = c.event_exercise_id
                 WHERE ee.event_id = sqlc.arg(event_id)
                   AND ee.exercise_id = d.exercise_id
                   AND c.task_id = d.task_id
                 LIMIT 1), '')::text                    AS challenge_name
FROM integrity_dismissals d
         LEFT JOIN users u ON u.id = d.created_by
WHERE (d.scope = 'event' AND d.event_id = sqlc.arg(event_id))
   OR (d.scope = 'exercise' AND d.exercise_id IN
                                (SELECT ee.exercise_id FROM event_exercises ee WHERE ee.event_id = sqlc.arg(event_id)))
ORDER BY d.created_at DESC, d.id;

-- name: CreateIntegrityDismissal :execrows
INSERT INTO integrity_dismissals (id, scope, event_id, exercise_id, task_id, kind, key, note, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(scope), sqlc.narg(event_id), sqlc.arg(exercise_id), sqlc.arg(task_id), sqlc.arg(kind),
        sqlc.arg(key), sqlc.arg(note), sqlc.narg(created_by), sqlc.arg(created_at))
ON CONFLICT DO NOTHING;

-- name: DeleteIntegrityDismissal :execrows
DELETE
FROM integrity_dismissals d
WHERE d.id = sqlc.arg(id)
  AND ((d.scope = 'event' AND d.event_id = sqlc.arg(event_id))
    OR (d.scope = 'exercise' AND d.exercise_id IN
                                 (SELECT ee.exercise_id FROM event_exercises ee WHERE ee.event_id = sqlc.arg(event_id))));
