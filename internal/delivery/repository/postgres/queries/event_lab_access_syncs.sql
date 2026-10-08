-- name: RequestEventLabAccessSync :one
INSERT INTO event_lab_access_syncs (event_team_id, desired_revision, applied_revision, updated_at)
VALUES (sqlc.arg(event_team_id), 1, 0, sqlc.arg(updated_at))
ON CONFLICT (event_team_id) DO UPDATE
SET desired_revision = event_lab_access_syncs.desired_revision + 1,
    operation_id=NULL, policy_fingerprint='', expected_group_uid='',
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
    operation_id=NULL, policy_fingerprint='', expected_group_uid='',
    updated_at = EXCLUDED.updated_at;

-- name: ListDirtyEventLabAccessSyncs :many
WITH access_state AS (
    SELECT sync.event_team_id,
           team.event_id,
           sync.desired_revision,
           sync.applied_revision, sync.operation_id, sync.policy_fingerprint, sync.expected_group_uid, sync.access_fence_vpn_boot_id,
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
    -- Teardown is terminal for this group; absence must not recreate it.
    WHERE rollout.torn_down_at IS NULL
)
SELECT event_team_id, event_id, desired_revision, applied_revision, operation_id, policy_fingerprint, expected_group_uid, updated_at, runtime_open, vpn_enabled, stage_epoch
FROM access_state
WHERE desired_revision > applied_revision
   OR applied_runtime_open IS DISTINCT FROM runtime_open
   OR applied_vpn_enabled IS DISTINCT FROM vpn_enabled
   OR applied_stage_epoch IS DISTINCT FROM stage_epoch
   -- An old applied revision is historical evidence only. Keep reconciling
   -- whenever its complete current certificate disappears or loses freshness.
   OR NOT EXISTS (SELECT 1 FROM lab_monitoring_current m,
      jsonb_array_elements(COALESCE(m.payload->'groups','[]'::jsonb)) g,
      jsonb_array_elements(COALESCE(m.payload->'policies','[]'::jsonb)) p
    WHERE m.event_team_id=access_state.event_team_id AND m.event_id=access_state.event_id
      AND access_state.operation_id IS NOT NULL
      AND access_state.operation_id<>'00000000-0000-0000-0000-000000000000'::uuid
      AND access_state.policy_fingerprint<>'' AND access_state.expected_group_uid<>''
      AND access_state.access_fence_vpn_boot_id<>''
      AND m.observed_at BETWEEN now()-interval '30 seconds' AND now()
      AND g->>'name'=m.lab_group_name AND g->>'uid'=access_state.expected_group_uid
      AND g->'status'->>'currentVpnBootAvailable'='true'
      AND g->'status'->>'currentVpnBootId'=access_state.access_fence_vpn_boot_id
      AND CASE WHEN g->'status'->>'currentVpnBootObservedUnixMs' ~ '^[0-9]{1,16}$' THEN (g->'status'->>'currentVpnBootObservedUnixMs')::bigint ELSE 0 END
          BETWEEN (extract(epoch FROM now())*1000)::bigint-30000 AND (extract(epoch FROM now())*1000)::bigint
      AND p->>'labGroupName'=m.lab_group_name AND p->>'expectedGroupUid'=access_state.expected_group_uid
      AND COALESCE(p->>'policyUid','')<>'' AND p->>'operationId'=access_state.operation_id::text
      AND p->>'desiredRevision'=access_state.desired_revision::text
      AND p->'status'->>'operationId'=access_state.operation_id::text
      AND p->'status'->>'appliedRevision'=access_state.desired_revision::text
      AND p->'status'->>'state'='Applied' AND COALESCE(p->'status'->>'lastError','')=''
      AND CASE WHEN p->>'generation' ~ '^[0-9]{1,16}$' THEN (p->>'generation')::bigint ELSE 0 END>0
      AND p->>'generation'=p->'status'->>'observedGeneration'
      AND CASE WHEN p->'status'->>'appliedAtUnixMs' ~ '^[0-9]{1,16}$' THEN (p->'status'->>'appliedAtUnixMs')::bigint ELSE 0 END>0
      AND p->'status'->>'vpnBootId'=access_state.access_fence_vpn_boot_id)
ORDER BY updated_at, event_team_id
LIMIT sqlc.arg(limit_val);

-- name: MarkEventLabAccessSyncApplied :execrows
UPDATE event_lab_access_syncs AS sync
SET applied_revision = sqlc.arg(desired_revision),
    access_fence_vpn_boot_id=sqlc.arg(access_fence_vpn_boot_id),
    runtime_open = sqlc.arg(runtime_open),
    vpn_enabled = sqlc.arg(vpn_enabled),
    applied_stage_epoch = sqlc.arg(stage_epoch),
    updated_at = sqlc.arg(updated_at)
WHERE sync.event_team_id = sqlc.arg(event_team_id)
  AND sync.desired_revision = sqlc.arg(desired_revision)
  AND sync.operation_id=sqlc.arg(operation_id)::uuid
  AND sync.policy_fingerprint=sqlc.arg(policy_fingerprint)
  AND sync.expected_group_uid=sqlc.arg(expected_group_uid)
  -- Recheck the current Monitoring tuple in the acknowledgement statement.
  -- A VPN restart after the worker read must not acknowledge the old boot.
  AND EXISTS (SELECT 1 FROM lab_monitoring_current m,
      jsonb_array_elements(COALESCE(m.payload->'groups','[]'::jsonb)) g,
      jsonb_array_elements(COALESCE(m.payload->'policies','[]'::jsonb)) p
    WHERE m.event_team_id=sqlc.arg(event_team_id) AND m.lab_group_name=sqlc.arg(lab_group_name)
      AND m.observed_at BETWEEN sqlc.arg(updated_at)::timestamptz-interval '30 seconds' AND sqlc.arg(updated_at)::timestamptz
      AND g->>'name'=m.lab_group_name AND g->>'uid'=sqlc.arg(expected_group_uid)
      AND g->'status'->>'currentVpnBootAvailable'='true'
      AND g->'status'->>'currentVpnBootId'=sqlc.arg(access_fence_vpn_boot_id)
      AND CASE WHEN g->'status'->>'currentVpnBootObservedUnixMs' ~ '^[0-9]{1,16}$' THEN (g->'status'->>'currentVpnBootObservedUnixMs')::bigint ELSE 0 END
          BETWEEN (extract(epoch FROM sqlc.arg(updated_at)::timestamptz)*1000)::bigint-30000 AND (extract(epoch FROM sqlc.arg(updated_at)::timestamptz)*1000)::bigint
      AND p->>'labGroupName'=m.lab_group_name AND p->>'expectedGroupUid'=sqlc.arg(expected_group_uid)
      AND COALESCE(p->>'policyUid','')<>'' AND p->>'operationId'=sqlc.arg(operation_id)::uuid::text
      AND p->>'desiredRevision'=sqlc.arg(desired_revision)::bigint::text
      AND p->'status'->>'operationId'=sqlc.arg(operation_id)::uuid::text
      AND p->'status'->>'appliedRevision'=sqlc.arg(desired_revision)::bigint::text
      AND p->'status'->>'state'='Applied' AND COALESCE(p->'status'->>'lastError','')=''
      AND CASE WHEN p->>'generation' ~ '^[0-9]{1,16}$' THEN (p->>'generation')::bigint ELSE 0 END>0
      AND p->>'generation'=p->'status'->>'observedGeneration'
      AND CASE WHEN p->'status'->>'appliedAtUnixMs' ~ '^[0-9]{1,16}$' THEN (p->'status'->>'appliedAtUnixMs')::bigint ELSE 0 END>0
      AND p->'status'->>'vpnBootId'=sqlc.arg(access_fence_vpn_boot_id))
  AND (sync.applied_revision < sqlc.arg(desired_revision)
       OR sync.runtime_open IS DISTINCT FROM sqlc.arg(runtime_open)
       OR sync.vpn_enabled IS DISTINCT FROM sqlc.arg(vpn_enabled)
       OR sync.applied_stage_epoch IS DISTINCT FROM sqlc.arg(stage_epoch)
       OR sync.access_fence_vpn_boot_id IS DISTINCT FROM sqlc.arg(access_fence_vpn_boot_id));

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
 AND (lb.lab_id IS NULL OR (canonical.desired_state='Running' AND canonical.logical_closed_at IS NULL))
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
LEFT JOIN event_team_labs canonical ON canonical.id=lb.lab_id
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
    operation_id=NULL, policy_fingerprint='', expected_group_uid='',
    updated_at = EXCLUDED.updated_at;

-- name: MaterializeEventLabAccessPolicy :one
-- A Request already allocated a new revision and reset its fingerprint. A
-- boundary-derived policy change allocates exactly one more revision before RPC.
UPDATE event_lab_access_syncs
SET desired_revision=desired_revision + CASE WHEN policy_fingerprint='' THEN 0 ELSE 1 END,
 operation_id=sqlc.arg(operation_id),policy_fingerprint=sqlc.arg(policy_fingerprint),
 expected_group_uid=sqlc.arg(expected_group_uid),updated_at=sqlc.arg(updated_at)
WHERE event_team_id=sqlc.arg(event_team_id)
 AND desired_revision=sqlc.arg(expected_revision)
 AND (policy_fingerprint<>sqlc.arg(policy_fingerprint) OR expected_group_uid<>sqlc.arg(expected_group_uid) OR operation_id IS NULL)
RETURNING *;

-- name: GetCurrentEventLabAccessMonitoring :one
SELECT m.* FROM lab_monitoring_current m JOIN event_teams t ON t.id=m.event_team_id AND t.event_id=m.event_id
WHERE m.event_team_id=sqlc.arg(event_team_id) ORDER BY m.observed_at DESC,m.lab_group_name LIMIT 1;
