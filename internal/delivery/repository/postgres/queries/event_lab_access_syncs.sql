-- name: RequestEventLabAccessSync :one
INSERT INTO event_lab_access_syncs (event_team_id, desired_revision, applied_revision, updated_at)
VALUES (sqlc.arg(event_team_id), 1, 0, sqlc.arg(updated_at))
ON CONFLICT (event_team_id) DO UPDATE
SET desired_revision = event_lab_access_syncs.desired_revision + 1,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: RequestEventLabAccessSyncsForEvent :exec
INSERT INTO event_lab_access_syncs (event_team_id, desired_revision, applied_revision, updated_at)
SELECT id, 1, 0, sqlc.arg(updated_at)
FROM event_teams team
WHERE team.event_id = sqlc.arg(event_id)
  -- The moderators team of an event without infrastructure has no VPN or
  -- Labs: it exists only for the moderators board and never syncs.
  AND (NOT team.moderators
    OR EXISTS (SELECT 1 FROM events event WHERE event.id = team.event_id AND event.infrastructure_allowed))
ON CONFLICT (event_team_id) DO UPDATE
SET desired_revision = event_lab_access_syncs.desired_revision + 1,
    updated_at = EXCLUDED.updated_at;

-- name: ListDirtyEventLabAccessSyncs :many
WITH access_state AS (
    SELECT sync.event_team_id,
           team.event_id,
           sync.desired_revision,
           sync.applied_revision,
           sync.updated_at,
           sync.runtime_open AS applied_runtime_open,
           sync.vpn_enabled AS applied_vpn_enabled,
           sync.applied_stage_epoch,
           -- Boundaries of the event's stages that have passed: the sync wakes at each one.
           event_stage_epoch(team.event_id, now()) AS stage_epoch,
           -- After the final stand teardown no team VPN group is recreated.
           COALESCE(event.infrastructure_allowed AND rollout.torn_down_at IS NULL, false)::boolean AS vpn_enabled,
           CASE WHEN event.lifecycle_configured
               AND event.available_from <= now()
               AND (event.archive_at IS NULL OR now() < event.archive_at)
               AND event.start_at <= now()
               AND (event.finish_at IS NULL OR now() < event.finish_at)
               AND (event.manual_finished_at IS NULL OR now() < event.manual_finished_at)
               AND (event.withdraw_at IS NULL OR now() < event.withdraw_at)
               THEN true ELSE false END AS runtime_open
    FROM event_lab_access_syncs sync
    JOIN event_teams team ON team.id = sync.event_team_id
    JOIN events event ON event.id = team.event_id
    LEFT JOIN event_stand_rollouts rollout ON rollout.event_id = event.id
)
SELECT event_team_id, event_id, desired_revision, applied_revision, updated_at, runtime_open, vpn_enabled, stage_epoch
FROM access_state
WHERE desired_revision > applied_revision
   OR applied_runtime_open IS DISTINCT FROM runtime_open
   OR applied_vpn_enabled IS DISTINCT FROM vpn_enabled
   OR applied_stage_epoch IS DISTINCT FROM stage_epoch
ORDER BY updated_at, event_team_id
LIMIT sqlc.arg(limit_val);

-- name: MarkEventLabAccessSyncApplied :execrows
UPDATE event_lab_access_syncs
SET applied_revision = sqlc.arg(desired_revision),
    runtime_open = sqlc.arg(runtime_open),
    vpn_enabled = sqlc.arg(vpn_enabled),
    applied_stage_epoch = sqlc.arg(stage_epoch),
    updated_at = sqlc.arg(updated_at)
WHERE event_team_id = sqlc.arg(event_team_id)
  AND desired_revision = sqlc.arg(desired_revision)
  AND (applied_revision < sqlc.arg(desired_revision)
       OR runtime_open IS DISTINCT FROM sqlc.arg(runtime_open)
       OR vpn_enabled IS DISTINCT FROM sqlc.arg(vpn_enabled)
       OR applied_stage_epoch IS DISTINCT FROM sqlc.arg(stage_epoch));

-- name: ListEventLabAccessClients :many
-- The hidden moderators team has no participant rows: its VPN clients are the
-- event owner and moderators (observers are read-only and get no client).
SELECT participant.user_id
FROM event_participants participant
WHERE participant.team_id = sqlc.arg(event_team_id)::uuid
  AND participant.status = 2
UNION
SELECT manager.user_id
FROM event_managers manager
JOIN event_teams team ON team.event_id = manager.event_id
WHERE team.id = sqlc.arg(event_team_id)::uuid
  AND team.moderators
  AND manager.role IN (0, 1)
ORDER BY user_id;

-- name: ListEventLabAccessLabs :many
-- A lab is open to a team's members only when the same locks as the lab link hold: the task is published, its
-- set is not detached and every prerequisite is solved by the team. The hidden moderators team tests tasks
-- before they are shown, so it keeps the readiness rule alone.
SELECT lb.lab_group_name,
       lb.lab_name,
       CASE WHEN lb.readiness = 1 AND tc.readiness = 2
                AND (team.moderators
                    OR (ec.published
                        AND ee.status <> 2
                        -- A staged set is reachable only while its stage is open, or ended but returnable.
                        AND event_stage_phase(stage.opens_at, stage.closes_at, stage.returnable, now()) IN (1, 2)
                        AND NOT EXISTS (SELECT 1
                                        FROM event_challenge_prerequisites prerequisite
                                        WHERE prerequisite.challenge_id = tc.event_challenge_id
                                          AND NOT EXISTS (SELECT 1
                                                          FROM team_challenges own
                                                          JOIN team_challenge_solves solved ON solved.team_challenge_id = own.id
                                                          WHERE own.event_team_id = tc.event_team_id
                                                            AND own.event_challenge_id = prerequisite.prerequisite_challenge_id))))
           THEN true ELSE false END AS available
FROM lab_bindings lb
JOIN team_challenges tc ON tc.event_team_id = lb.event_team_id
                       AND tc.event_challenge_id = lb.event_challenge_id
JOIN event_teams team ON team.id = lb.event_team_id
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
JOIN event_exercises ee ON ee.id = ec.event_exercise_id
LEFT JOIN event_stages stage ON stage.id = ee.stage_id
WHERE lb.event_team_id = sqlc.arg(event_team_id)
  AND lb.readiness <> 3
ORDER BY lb.lab_name;

-- name: RequestModeratorsTeamLabAccessSync :exec
-- Manager changes alter the moderators team's VPN client set.
INSERT INTO event_lab_access_syncs (event_team_id, desired_revision, applied_revision, updated_at)
SELECT id, 1, 0, sqlc.arg(updated_at)
FROM event_teams team
WHERE team.event_id = sqlc.arg(event_id)
  AND team.moderators
  AND EXISTS (SELECT 1 FROM events event WHERE event.id = team.event_id AND event.infrastructure_allowed)
ON CONFLICT (event_team_id) DO UPDATE
SET desired_revision = event_lab_access_syncs.desired_revision + 1,
    updated_at = EXCLUDED.updated_at;
