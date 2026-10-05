-- name: CreateTeamChallenge :one
INSERT INTO team_challenges (id, event_id, event_team_id, event_challenge_id, variant_index, snapshot, expected_flag, readiness, created_at,
                             hints)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(event_team_id), sqlc.arg(event_challenge_id), sqlc.arg(variant_index),
        sqlc.arg(snapshot), sqlc.arg(expected_flag), sqlc.arg(readiness), sqlc.arg(created_at),
        COALESCE(sqlc.narg(hints)::jsonb, '[]'::jsonb))
RETURNING *;

-- name: GetTeamChallenge :one
SELECT tc.id, tc.event_id, tc.event_team_id, tc.event_challenge_id, tc.variant_index,
       tc.snapshot, tc.expected_flag, tc.readiness, solved.solved_at, tc.created_at
FROM team_challenges tc
LEFT JOIN team_challenge_solves solved ON solved.team_challenge_id = tc.id
WHERE tc.event_team_id = sqlc.arg(event_team_id)
  AND tc.event_challenge_id = sqlc.arg(event_challenge_id)
FOR UPDATE OF tc;

-- name: ListTeamChallenges :many
SELECT tc.id, tc.event_id, tc.event_team_id, tc.event_challenge_id, tc.variant_index,
       tc.snapshot, tc.expected_flag, tc.readiness, solved.solved_at, tc.created_at
FROM team_challenges tc
LEFT JOIN team_challenge_solves solved ON solved.team_challenge_id = tc.id
WHERE tc.event_team_id = sqlc.arg(event_team_id)
ORDER BY tc.created_at ASC, tc.id ASC;

-- name: ListTeamBoardChallenges :many
-- The team's board: every assignment with its current presentation metadata.
-- published_only keeps the participant board to board-published challenges;
-- the moderators board passes false to also see unpublished ones.
-- infrastructure: the assignment has a Lab binding (a stand challenge).
-- points: the static value (the event's one when the challenge follows a
-- static event); board_position: the place inside the group, the order the
-- manage page sets (board_order), then by set attach time and set order.
SELECT tc.id, tc.event_id, tc.event_team_id, tc.event_challenge_id, tc.variant_index,
       tc.snapshot, tc.expected_flag, tc.readiness, solved.solved_at, tc.created_at,
       tc.content_updated_at, tc.hints AS team_hints,
       ee.stage_id,
       event_stage_phase(stage.opens_at, stage.closes_at, stage.returnable, sqlc.arg(at)::timestamptz) AS stage_phase,
       (practice.team_challenge_id IS NOT NULL)::boolean AS practice_solved,
       (CASE WHEN e.static_points IS NOT NULL AND e.scoring_mode = 0
                  AND (e.force_event_scoring OR ec.scoring_mode IS NULL)
             THEN e.static_points ELSE ec.points END)::integer AS points,
       ec.order_index, ec.group_id, ec.hints_enabled, ec.published,
       ec.hints AS board_hints, ec.hint_costs,
       COALESCE(ecg.name, 'Інші')::text AS group_name,
       COALESCE(ecg.order_index, 2147483647)::integer AS group_order,
       (row_number() OVER (PARTITION BY ec.group_id
                           ORDER BY ec.board_order ASC NULLS LAST, ee.created_at ASC, ec.order_index ASC, tc.id ASC) - 1)::integer AS board_position,
       EXISTS (SELECT 1
               FROM lab_bindings lb
               WHERE lb.event_team_id = tc.event_team_id
                 AND lb.event_challenge_id = tc.event_challenge_id)::boolean AS infrastructure
FROM team_challenges tc
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.status <> 2
JOIN events e ON e.id = tc.event_id
LEFT JOIN event_challenge_groups ecg ON ecg.id = ec.group_id
LEFT JOIN team_challenge_solves solved ON solved.team_challenge_id = tc.id
LEFT JOIN team_challenge_practice_solves practice ON practice.team_challenge_id = tc.id
LEFT JOIN event_stages stage ON stage.id = ee.stage_id
WHERE tc.event_team_id = sqlc.arg(event_team_id)
  AND (ec.published OR NOT sqlc.arg(published_only)::boolean)
  -- A task of an upcoming stage is hidden from participants entirely; the moderators board keeps everything.
  AND (NOT sqlc.arg(published_only)::boolean
    OR event_stage_phase(stage.opens_at, stage.closes_at, stage.returnable, sqlc.arg(at)::timestamptz) <> 0)
ORDER BY group_order ASC, board_position ASC;

-- name: ListTeamChallengePrerequisites :many
-- Every prerequisite edge of the team's assigned challenges, named by the
-- team's own snapshot of the prerequisite (fallback: the board snapshot) and
-- marked solved when the team solved it.
SELECT prerequisite.challenge_id,
       prerequisite.prerequisite_challenge_id,
       COALESCE(own.snapshot->>'name', board.snapshot->>'name', '')::text AS name,
       (solved.solved_at IS NOT NULL)::boolean AS solved
FROM team_challenges tc
JOIN event_challenge_prerequisites prerequisite ON prerequisite.challenge_id = tc.event_challenge_id
JOIN event_challenges board ON board.id = prerequisite.prerequisite_challenge_id
LEFT JOIN team_challenges own ON own.event_team_id = tc.event_team_id
                             AND own.event_challenge_id = prerequisite.prerequisite_challenge_id
LEFT JOIN team_challenge_solves solved ON solved.team_challenge_id = own.id
WHERE tc.event_team_id = sqlc.arg(event_team_id)
ORDER BY prerequisite.challenge_id, board.order_index, prerequisite.prerequisite_challenge_id;

-- name: CountEventChallengeSolves :many
-- Accepted solves per board challenge by admitted, non-hidden teams, plus the
-- caller's own team even when it is hidden or not admitted. A cutoff (freeze)
-- keeps only other teams' solves before it.
SELECT tc.event_challenge_id, count(*)::bigint AS solves
FROM team_challenge_solves solved
JOIN team_challenges tc ON tc.id = solved.team_challenge_id
JOIN event_teams team ON team.id = tc.event_team_id
WHERE tc.event_id = sqlc.arg(event_id)
  AND (team.id = sqlc.arg(own_team_id)::uuid
    OR (event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually,
                            team.admission_locked, team.member_count)
        AND (sqlc.narg(cutoff)::timestamptz IS NULL OR solved.solved_at < sqlc.narg(cutoff)::timestamptz)))
GROUP BY tc.event_challenge_id;

-- name: ListEventChallengeSolves :many
-- One page of who solved a board challenge: the same team population as the
-- solve counts, named like the public scoreboard names its rows. Oldest solve
-- first; the cursor is the solve id (team_challenge_id) and the keyset is
-- (solved_at, team_challenge_id), so a page never skips or repeats a row. first_blood is read-time: the earliest solve among the
-- visible teams.
SELECT team.id AS event_team_id,
       event_team_public_name(team.individual, team.event_id, team.captain_id, team.name)::text AS team_name,
       (team.individual AND NOT EXISTS (SELECT 1
                                    FROM event_participants named
                                    JOIN event_configs named_config ON named_config.event_id = named.event_id
                                    WHERE named.event_id = team.event_id AND named.user_id = team.captain_id
                                      AND named_config.allow_pseudonyms AND NULLIF(btrim(named.pseudonym), '') IS NOT NULL))::boolean AS name_is_real,
       solved.solved_at,
       tc.id AS team_challenge_id,
       (visible.is_visible
        AND NOT EXISTS (SELECT 1
                        FROM team_challenge_solves other
                        JOIN team_challenges other_tc ON other_tc.id = other.team_challenge_id
                        JOIN event_teams other_team ON other_team.id = other_tc.event_team_id
                        WHERE other_tc.event_challenge_id = tc.event_challenge_id
                          AND event_team_visible(other_team.hidden, other_team.moderators, other_team.event_id, other_team.individual,
                                                 other_team.admitted_manually, other_team.admission_locked, other_team.member_count)
                          AND (other.solved_at, other.team_challenge_id) < (solved.solved_at, tc.id)))::boolean AS first_blood
FROM team_challenge_solves solved
JOIN team_challenges tc ON tc.id = solved.team_challenge_id
JOIN event_teams team ON team.id = tc.event_team_id
CROSS JOIN LATERAL (SELECT event_team_visible(team.hidden, team.moderators, team.event_id, team.individual,
                                               team.admitted_manually, team.admission_locked, team.member_count) AS is_visible) visible
WHERE tc.event_id = sqlc.arg(event_id)
  AND tc.event_challenge_id = sqlc.arg(event_challenge_id)
  AND (team.id = sqlc.arg(own_team_id)::uuid
    OR (visible.is_visible
        AND (sqlc.narg(cutoff)::timestamptz IS NULL OR solved.solved_at < sqlc.narg(cutoff)::timestamptz)))
  AND (sqlc.narg(after_id)::uuid IS NULL
    OR (solved.solved_at, tc.id) > (SELECT after_solve.solved_at, after_solve.team_challenge_id
                                    FROM team_challenge_solves after_solve
                                    WHERE after_solve.team_challenge_id = sqlc.narg(after_id)::uuid))
ORDER BY solved.solved_at ASC, tc.id ASC
LIMIT sqlc.arg(row_limit);

-- name: ListFileSizes :many
-- Batch size lookup for board attachments; missing files are simply absent.
SELECT id, size_bytes
FROM files
WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ListEventChallengeAvailability :many
-- Board configuration and preparation state are distinct: every configured
-- challenge is returned, including those with no materialized team rows yet.
SELECT ec.id AS event_challenge_id,
       count(tc.id)::bigint AS total,
       count(tc.id) FILTER (WHERE tc.readiness = 0)::bigint AS preparing,
       count(tc.id) FILTER (WHERE tc.readiness = 1)::bigint AS ready,
       count(tc.id) FILTER (WHERE tc.readiness = 2)::bigint AS available,
       count(tc.id) FILTER (WHERE tc.readiness = 3 OR lb.readiness = 2)::bigint AS failed
FROM event_challenges ec
LEFT JOIN team_challenges tc ON tc.event_challenge_id = ec.id
LEFT JOIN lab_bindings lb ON lb.event_team_id = tc.event_team_id
                         AND lb.event_challenge_id = tc.event_challenge_id
WHERE ec.event_exercise_id = sqlc.arg(event_exercise_id)
GROUP BY ec.id;

-- name: UpdateTeamChallengeReadiness :execrows
UPDATE team_challenges
SET readiness = sqlc.arg(readiness)
WHERE id = sqlc.arg(id)
  AND readiness = sqlc.arg(expected_readiness);

-- name: PublishAvailableTeamChallenges :many
-- Ready -> Published for board-published challenges. A challenge without a lab
-- binding is static and opens at once; one with a binding is infrastructure
-- and opens only when the caller passes the event's strict barrier.
UPDATE team_challenges tc
SET readiness = 2
FROM event_challenges ec
WHERE tc.event_id = sqlc.arg(event_id)
  AND ec.id = tc.event_challenge_id
  AND ec.published
  AND tc.readiness = 1
  AND EXISTS (SELECT 1
              FROM event_teams team
              WHERE team.id = tc.event_team_id
                AND (team.moderators OR event_team_formed(team.event_id, team.formed_at)))
  AND (sqlc.arg(labs_open)::boolean
    OR NOT EXISTS (SELECT 1
                   FROM lab_bindings lb
                   WHERE lb.event_team_id = tc.event_team_id
                     AND lb.event_challenge_id = tc.event_challenge_id))
RETURNING tc.event_team_id;

-- name: ListTeamChallengesForRefresh :many
-- Team assignments of board challenges whose source content is switched.
SELECT tc.id, tc.event_team_id, tc.event_challenge_id, tc.variant_index, tc.snapshot, tc.hints,
       tc.expected_flag, (solved.solved_at IS NOT NULL)::boolean AS solved
FROM team_challenges tc
LEFT JOIN team_challenge_solves solved ON solved.team_challenge_id = tc.id
WHERE tc.event_challenge_id = ANY (sqlc.arg(ids)::uuid[])
ORDER BY tc.id
FOR UPDATE OF tc;

-- name: UpdateTeamChallengeContent :execrows
-- content_changed marks a participant-visible change («Оновлено»).
UPDATE team_challenges
SET variant_index      = sqlc.arg(variant_index),
    snapshot           = sqlc.arg(snapshot),
    hints              = sqlc.arg(hints),
    expected_flag      = sqlc.arg(expected_flag),
    content_updated_at = CASE WHEN sqlc.arg(content_changed)::boolean THEN sqlc.arg(now)::timestamptz
                              ELSE content_updated_at END
WHERE id = sqlc.arg(id);

-- name: GetTeamChallengeHints :one
-- One assignment's hint texts plus the board's canonical hints and costs.
SELECT tc.id, tc.event_id, tc.event_team_id, tc.event_challenge_id, tc.readiness, tc.hints AS team_hints,
       ec.hints AS board_hints, ec.hint_costs, ec.hints_enabled, ec.published,
       (solved.solved_at IS NOT NULL)::boolean AS solved,
       event_stage_phase(stage.opens_at, stage.closes_at, stage.returnable, sqlc.arg(at)::timestamptz) AS stage_phase
FROM team_challenges tc
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.status <> 2
LEFT JOIN event_stages stage ON stage.id = ee.stage_id
LEFT JOIN team_challenge_solves solved ON solved.team_challenge_id = tc.id
WHERE tc.event_team_id = sqlc.arg(event_team_id)
  AND tc.event_challenge_id = sqlc.arg(event_challenge_id)
FOR UPDATE OF tc;

-- name: CreateHintUnlock :one
-- Idempotent per team and hint: a repeated unlock returns the first one.
WITH inserted AS (
    INSERT INTO team_challenge_hint_unlocks (team_challenge_id, hint_id, event_id, event_team_id, event_challenge_id,
                                             unlocked_by, unlocked_at, cost)
    VALUES (sqlc.arg(team_challenge_id), sqlc.arg(hint_id), sqlc.arg(event_id), sqlc.arg(event_team_id),
            sqlc.arg(event_challenge_id), sqlc.narg(unlocked_by), sqlc.arg(unlocked_at), sqlc.arg(cost))
    ON CONFLICT (team_challenge_id, hint_id) DO NOTHING
    RETURNING team_challenge_id, hint_id, unlocked_by, unlocked_at, cost, true AS created
)
SELECT inserted.team_challenge_id, inserted.hint_id, inserted.unlocked_by, inserted.unlocked_at, inserted.cost, inserted.created
FROM inserted
UNION ALL
SELECT existing.team_challenge_id, existing.hint_id, existing.unlocked_by, existing.unlocked_at, existing.cost, false
FROM team_challenge_hint_unlocks existing
WHERE existing.team_challenge_id = sqlc.arg(team_challenge_id)
  AND existing.hint_id = sqlc.arg(hint_id)
  AND NOT EXISTS (SELECT 1 FROM inserted);

-- name: ListTeamHintUnlocks :many
SELECT unlock.team_challenge_id, unlock.event_challenge_id, unlock.hint_id, unlock.unlocked_at, unlock.cost,
       COALESCE(NULLIF(btrim(concat_ws(' ', person.first_name, person.last_name)), ''), '')::text AS unlocked_by_name
FROM team_challenge_hint_unlocks unlock
LEFT JOIN users person ON person.id = unlock.unlocked_by
WHERE unlock.event_team_id = sqlc.arg(event_team_id)
ORDER BY unlock.unlocked_at, unlock.hint_id;

-- name: ListEventHintUnlocks :many
-- Who unlocked which hint (moderators): team public name, member, cost.
SELECT unlock.event_team_id, unlock.event_challenge_id, unlock.hint_id, unlock.unlocked_at, unlock.cost,
       unlock.unlocked_by,
       event_team_public_name(team.individual, team.event_id, team.captain_id, team.name)::text AS team_name,
       COALESCE(ec.snapshot->>'name', '')::text AS challenge_name,
       ec.hints AS board_hints,
       COALESCE(NULLIF(btrim(concat_ws(' ', person.first_name, person.last_name)), ''), person.email, '')::text AS unlocked_by_name
FROM team_challenge_hint_unlocks unlock
JOIN event_teams team ON team.id = unlock.event_team_id
JOIN event_challenges ec ON ec.id = unlock.event_challenge_id
LEFT JOIN users person ON person.id = unlock.unlocked_by
WHERE unlock.event_id = sqlc.arg(event_id)
ORDER BY unlock.unlocked_at DESC, unlock.hint_id
LIMIT 1000;
