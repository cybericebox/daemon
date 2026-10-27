-- Data retention (Privacy Policy, section "Retention"). Every purge is a
-- set-based statement over at most batch_size rows, so a run holds short
-- locks and the job loops until a batch comes back short. SKIP LOCKED lets a
-- concurrent writer (or a second pass) keep its rows; the next run gets them.
-- Each statement is idempotent: a re-run matches nothing already purged.

-- name: PurgeExpiredSessions :execrows
DELETE FROM sessions
WHERE id IN (SELECT s.id
             FROM sessions s
             WHERE s.expires_at < sqlc.arg(expired_before)
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE SKIP LOCKED);

-- name: PurgeEventLabObservations :execrows
-- Raw rows past the telemetry period; only archived ones (see
-- PurgeRolledUpEventLabObservations), so nothing is lost before the archive.
DELETE FROM event_lab_observations
WHERE id IN (SELECT o.id
             FROM event_lab_observations o
             JOIN event_analytics_rollups r ON r.event_id = o.event_id
             WHERE o.observed_at < sqlc.arg(observed_before)
               AND (r.lab_telemetry_hold_until IS NULL OR r.lab_telemetry_hold_until <= now())
               AND r.lab_archive_cursor_id IS NOT NULL
               AND (o.received_at, o.id) <= (r.lab_archive_cursor_received_at, r.lab_archive_cursor_id)
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE SKIP LOCKED);

-- name: PurgeRolledUpEventLabObservations :execrows
-- Raw lab snapshots are large, so they go a day after arrival once every
-- rollup that reads them (VPN sessions, lab usage, the transitions journal) has
-- passed them and the archive holds them (a raw row is never deleted before it
-- is archived). The latest snapshot of each lab group stays for the latest-state readers.
-- Events without infrastructure have no VPN rollup to wait for.
DELETE FROM event_lab_observations
WHERE id IN (SELECT o.id
             FROM event_lab_observations o
             JOIN events e ON e.id = o.event_id
             JOIN event_analytics_rollups r ON r.event_id = o.event_id
             WHERE o.received_at < sqlc.arg(received_before)
               AND (r.lab_telemetry_hold_until IS NULL OR r.lab_telemetry_hold_until <= now())
               AND r.lab_usage_cursor_id IS NOT NULL
               AND (o.received_at, o.id) <= (r.lab_usage_cursor_received_at, r.lab_usage_cursor_id)
               AND r.lab_journal_cursor_id IS NOT NULL
               AND (o.received_at, o.id) <= (r.lab_journal_cursor_received_at, r.lab_journal_cursor_id)
               AND r.lab_archive_cursor_id IS NOT NULL
               AND (o.received_at, o.id) <= (r.lab_archive_cursor_received_at, r.lab_archive_cursor_id)
               AND (NOT e.infrastructure_allowed
                    OR (r.vpn_cursor_id IS NOT NULL
                        AND (o.received_at, o.id) <= (r.vpn_cursor_received_at, r.vpn_cursor_id)))
               AND EXISTS (SELECT 1
                           FROM event_lab_observations n
                           WHERE n.event_id = o.event_id
                             AND n.event_team_id = o.event_team_id
                             AND n.lab_group_name = o.lab_group_name
                             AND (n.observed_at, n.id) > (o.observed_at, o.id))
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF o SKIP LOCKED);

-- Derived lab telemetry (minute tables, transitions journal and its states, raw
-- archive) is kept for the telemetry period after the event's access closes:
-- the withdrawal if set, else the finish (manual or scheduled). An event with
-- no end (permanent, not finished) has nothing closed: its rows go by their
-- own time (the minute, the transition, the end of the archived hour), a
-- rolling window. closed_before is the cutoff (now minus the period) for both.

-- name: PurgeEventLabUsageMinutes :execrows
DELETE FROM event_lab_usage_minutes
WHERE ctid IN (SELECT m.ctid
               FROM event_lab_usage_minutes m
               JOIN events e ON e.id = m.event_id
               WHERE COALESCE(e.withdraw_at, LEAST(e.manual_finished_at, e.finish_at), m.bucket_at) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF m SKIP LOCKED);

-- name: PurgeEventLabMinutes :execrows
DELETE FROM event_lab_minutes
WHERE ctid IN (SELECT m.ctid
               FROM event_lab_minutes m
               JOIN events e ON e.id = m.event_id
               WHERE COALESCE(e.withdraw_at, LEAST(e.manual_finished_at, e.finish_at), m.bucket_at) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF m SKIP LOCKED);

-- name: PurgeEventVPNClientMinutes :execrows
DELETE FROM event_vpn_client_minutes
WHERE ctid IN (SELECT m.ctid
               FROM event_vpn_client_minutes m
               JOIN events e ON e.id = m.event_id
               WHERE COALESCE(e.withdraw_at, LEAST(e.manual_finished_at, e.finish_at), m.bucket_at) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF m SKIP LOCKED);

-- name: PurgeEventLabTransitions :execrows
DELETE FROM event_lab_transitions
WHERE id IN (SELECT t.id
             FROM event_lab_transitions t
             JOIN events e ON e.id = t.event_id
             WHERE COALESCE(e.withdraw_at, LEAST(e.manual_finished_at, e.finish_at), t.at) < sqlc.arg(closed_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF t SKIP LOCKED);

-- name: PurgeEventLabJournalStates :execrows
-- The remembered states have no time of their own: they go with a closed event only.
DELETE FROM event_lab_journal_states
WHERE ctid IN (SELECT s.ctid
               FROM event_lab_journal_states s
               JOIN events e ON e.id = s.event_id
               WHERE COALESCE(e.withdraw_at, LEAST(e.manual_finished_at, e.finish_at)) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF s SKIP LOCKED);

-- name: PurgeEventLabObservationArchives :execrows
DELETE FROM event_lab_observation_archives
WHERE id IN (SELECT a.id
             FROM event_lab_observation_archives a
             JOIN events e ON e.id = a.event_id
             WHERE COALESCE(e.withdraw_at, LEAST(e.manual_finished_at, e.finish_at), a.period_end) < sqlc.arg(closed_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF a SKIP LOCKED);

-- name: PurgePlatformLabCapacityObservations :execrows
DELETE FROM platform_lab_capacity_observations
WHERE id IN (SELECT o.id
             FROM platform_lab_capacity_observations o
             WHERE o.observed_at < sqlc.arg(observed_before)
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE SKIP LOCKED);

-- name: PurgeNotificationDispatches :execrows
-- The delivery journal: a dispatch and (cascade) its per-channel targets,
-- which hold the recipient address and transport errors.
DELETE FROM notification_dispatches
WHERE id IN (SELECT d.id
             FROM notification_dispatches d
             WHERE d.created_at < sqlc.arg(created_before)
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE SKIP LOCKED);

-- name: PurgeFinishedSignals :execrows
-- Signals finished (delivered to every hook, or given up) before the cutoff;
-- their hook executions go with them (cascade).
DELETE FROM signal_outbox
WHERE id IN (SELECT s.id
             FROM signal_outbox s
             WHERE s.status IN ('completed', 'failed')
               AND s.created_at < sqlc.arg(created_before)
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE SKIP LOCKED);

-- name: PurgeEventFormAnswers :execrows
-- Answers of events that ended before the cutoff. The end is the effective
-- finish (the earlier of the scheduled and the manual finish); an event that
-- has not ended keeps its answers.
DELETE FROM event_form_answers
WHERE (form_version_id, user_id) IN (
    SELECT a.form_version_id, a.user_id
    FROM event_form_answers a
    JOIN events e ON e.id = a.event_id
    WHERE e.lifecycle_configured
      AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(ended_before)::timestamptz
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE OF a SKIP LOCKED);

-- name: PurgeEventActivity :execrows
-- The event activity log of events that ended before the cutoff (the same
-- effective finish as PurgeEventFormAnswers).
DELETE FROM event_activity
WHERE id IN (SELECT a.id
             FROM event_activity a
             JOIN events e ON e.id = a.event_id
             WHERE e.lifecycle_configured
               AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(ended_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF a SKIP LOCKED);

-- name: PurgeEventStandTransitions :execrows
-- Stand and laboratory transitions of events that ended before the cutoff.
DELETE FROM event_stand_transitions
WHERE id IN (SELECT t.id
             FROM event_stand_transitions t
             JOIN events e ON e.id = t.event_id
             WHERE e.lifecycle_configured
               AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(ended_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF t SKIP LOCKED);

-- name: PurgeEventVPNSessions :execrows
-- VPN sessions of events that ended before the cutoff.
DELETE FROM event_vpn_sessions
WHERE id IN (SELECT v.id
             FROM event_vpn_sessions v
             JOIN events e ON e.id = v.event_id
             WHERE e.lifecycle_configured
               AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(ended_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF v SKIP LOCKED);

-- name: PurgeEventAnswerFiles :execrows
-- Files attached to answers of events that ended before the cutoff (the same
-- rule as PurgeEventFormAnswers) and uploads never used in an answer. The
-- references go with them; references whose row is already gone (a deleted
-- event, team or account) are released too, so media GC reclaims the blobs.
WITH doomed AS (
    DELETE FROM event_answer_files
    WHERE file_id IN (
        SELECT f.file_id
        FROM event_answer_files f
        JOIN events e ON e.id = f.event_id
        WHERE (e.lifecycle_configured AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(ended_before)::timestamptz)
           OR (f.attached_at IS NULL AND f.created_at < sqlc.arg(pending_before)::timestamptz)
        LIMIT sqlc.arg(batch_size)
        FOR UPDATE OF f SKIP LOCKED)
    RETURNING file_id)
DELETE FROM file_references r
WHERE r.ref_type = 'event_answer_file'
  AND (r.ref_id IN (SELECT file_id FROM doomed)
    OR NOT EXISTS (SELECT 1 FROM event_answer_files a WHERE a.file_id = r.ref_id));

-- name: PurgeDeletedAccountsPersonalData :execrows
-- Residual personal data of deleted accounts. Deletion already scrubbed the
-- user row (name, email, password, picture) and cut sessions and providers;
-- this removes everything else that belongs to the person and anonymizes
-- their public alias. The tombstone row stays: challenge attempts, teams and
-- scoreboards keep referencing it, and it is presented as the generic
-- "Учасник" (event_participant_public_name) once the pseudonym is gone.
WITH batch AS (
    SELECT u.id
    FROM users u
    WHERE u.deleted_at IS NOT NULL
      AND u.personal_data_purged_at IS NULL
    ORDER BY u.deleted_at, u.id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
), form_answers AS (
    DELETE FROM event_form_answers WHERE user_id IN (SELECT id FROM batch)
), answer_files AS (
    -- Their own answers' files and pending uploads; the next
    -- PurgeEventAnswerFiles run releases the references.
    DELETE FROM event_answer_files
    WHERE (scope = 'participant' AND owner_id IN (SELECT id FROM batch))
       OR (attached_at IS NULL AND uploaded_by IN (SELECT id FROM batch))
), form_deliveries AS (
    DELETE FROM event_form_deliveries WHERE user_id IN (SELECT id FROM batch)
), vpn_configs AS (
    DELETE FROM user_vpn_configs WHERE user_id IN (SELECT id FROM batch)
), inbox AS (
    DELETE FROM in_app_notifications WHERE user_id IN (SELECT id FROM batch)
), notification_preferences AS (
    DELETE FROM notification_user_settings WHERE user_id IN (SELECT id FROM batch)
), dispatches AS (
    DELETE FROM notification_dispatches WHERE recipient_user_id IN (SELECT id FROM batch)
), envelopes AS (
    DELETE FROM secret_envelopes WHERE recipient_user_id IN (SELECT id FROM batch)
), idempotency AS (
    DELETE FROM request_idempotency WHERE owner_id IN (SELECT id FROM batch)
), managers AS (
    DELETE FROM event_managers WHERE user_id IN (SELECT id FROM batch)
), user_sessions AS (
    DELETE FROM sessions WHERE user_id IN (SELECT id FROM batch)
), providers AS (
    DELETE FROM user_providers WHERE user_id IN (SELECT id FROM batch)
), avatars AS (
    DELETE FROM file_references WHERE ref_type = 'user_avatar' AND ref_id IN (SELECT id FROM batch)
), signals AS (
    -- Signal history keeps the event context but not the person: references
    -- to the account become the nil UUID, personal fields are dropped.
    UPDATE signal_outbox
    SET payload = signal_payload_anonymized(payload, ARRAY(SELECT id::text FROM batch))
    WHERE payload ->> 'subject_user_id' IN (SELECT id::text FROM batch)
       OR payload ->> 'actor_user_id' IN (SELECT id::text FROM batch)
    RETURNING id
), signal_errors AS (
    -- Hook errors may quote the payload (an address, a name).
    UPDATE signal_hook_executions
    SET last_error = ''
    WHERE signal_id IN (SELECT id FROM signals)
      AND last_error <> ''
), activity AS (
    -- The event activity log keeps what happened, not who did it.
    UPDATE event_activity SET user_id = NULL WHERE user_id IN (SELECT id FROM batch)
), vpn_sessions AS (
    UPDATE event_vpn_sessions SET user_id = NULL, client_name = '' WHERE user_id IN (SELECT id FROM batch)
), lab_erasures AS (
    -- Only the link to the person goes: the client name becomes a pseudonym, the same everywhere (the IPs and the
    -- traffic stay). The archive is rewritten later (it needs the chunk unpacked): the account is queued with the
    -- events it took part in. The sub-selects see the rows as they are before this statement renames them.
    INSERT INTO lab_telemetry_erasures (user_id, client_name, pseudonym, event_ids)
    SELECT b.id, lab_client_name(b.id), 'deleted-' || gen_random_uuid()::text,
           ARRAY(SELECT x.event_id
                 FROM (SELECT p.event_id FROM event_participants p WHERE p.user_id = b.id
                       UNION
                       SELECT m.event_id FROM event_vpn_client_minutes m WHERE m.client_name = lab_client_name(b.id)
                       UNION
                       SELECT v.event_id FROM event_vpn_sessions v WHERE v.user_id = b.id) x)
    FROM batch b
    ON CONFLICT (user_id) DO NOTHING
    RETURNING user_id, client_name, pseudonym, event_ids
), lab_client_minutes AS (
    UPDATE event_vpn_client_minutes m SET client_name = e.pseudonym
    FROM lab_erasures e WHERE m.client_name = e.client_name
), lab_transitions AS (
    UPDATE event_lab_transitions t SET client_name = e.pseudonym
    FROM lab_erasures e WHERE t.client_name = e.client_name
), lab_client_states AS (
    UPDATE event_lab_journal_states s SET name = e.pseudonym
    FROM lab_erasures e WHERE s.kind = 'client' AND s.name = e.client_name
), lab_raw AS (
    UPDATE event_lab_observations o
    SET payload = replace(o.payload::text, e.client_name, e.pseudonym)::jsonb
    FROM lab_erasures e
    WHERE o.event_id = ANY (e.event_ids) AND o.payload::text LIKE '%' || e.client_name || '%'
), lab_current AS (
    UPDATE lab_monitoring_current c
    SET payload = replace(c.payload::text, e.client_name, e.pseudonym)::jsonb
    FROM lab_erasures e
    WHERE c.event_id = ANY (e.event_ids) AND c.payload::text LIKE '%' || e.client_name || '%'
), lab_touches AS (
    -- Lab access counters keep what happened to the task, not who did it.
    UPDATE event_lab_touches SET user_id = NULL WHERE user_id IN (SELECT id FROM batch)
), pseudonyms AS (
    UPDATE event_participants
    SET pseudonym = NULL
    WHERE user_id IN (SELECT id FROM batch)
      AND pseudonym IS NOT NULL
), temporal AS (
    -- Reset and confirmation links still in the mailbox; an email change code holds the new address.
    DELETE FROM temporal_codes WHERE data ->> 'UserID' IN (SELECT id::text FROM batch)
), presence AS (
    DELETE FROM event_participant_presence WHERE user_id IN (SELECT id FROM batch)
)
UPDATE users
SET personal_data_purged_at = sqlc.arg(purged_at)::timestamptz,
    inactivity_warned_at    = NULL
WHERE id IN (SELECT id FROM batch);

-- name: PurgeExpiredTemporalCodes :execrows
-- One-time codes (reset, confirmation, email change, setup links) are useless after their expiry; an email
-- change code holds an address, so none stays longer than needed.
DELETE FROM temporal_codes doomed
WHERE doomed.id IN (SELECT code.id
                    FROM temporal_codes code
                    WHERE code.expires_at < sqlc.arg(expired_before)
                    ORDER BY code.expires_at, code.id
                    LIMIT sqlc.arg(batch_size));

-- name: ClearReturnedInactivityWarnings :execrows
-- A warned user who signed in again is active: forget the warning so the
-- deletion never fires and a later inactivity period warns afresh.
UPDATE users
SET inactivity_warned_at = NULL
WHERE deleted_at IS NULL
  AND inactivity_warned_at IS NOT NULL
  AND last_seen > inactivity_warned_at;

-- name: ListInactiveAccountsToWarn :many
-- super_admin accounts are never removed automatically (lockout guard).
SELECT u.id, u.first_name, u.last_seen
FROM users u
WHERE u.deleted_at IS NULL
  AND u.inactivity_warned_at IS NULL
  AND u.last_seen < sqlc.arg(inactive_since)
  AND u.role <> 'super_admin'
ORDER BY u.last_seen, u.id
LIMIT sqlc.arg(batch_size);

-- name: MarkInactivityWarned :execrows
UPDATE users
SET inactivity_warned_at = sqlc.arg(warned_at)::timestamptz
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL
  AND inactivity_warned_at IS NULL;

-- name: ListInactiveAccountsToDelete :many
-- Warned before the grace cutoff and not seen since the warning.
SELECT u.id, u.inactivity_warned_at::timestamptz AS warned_at
FROM users u
WHERE u.deleted_at IS NULL
  AND u.inactivity_warned_at < sqlc.arg(warned_before)::timestamptz
  AND u.last_seen <= u.inactivity_warned_at
  AND u.role <> 'super_admin'
ORDER BY u.inactivity_warned_at, u.id
LIMIT sqlc.arg(batch_size);

-- name: PurgeExpiredEventInvitations :execrows
-- Pending invitations that can no longer be accepted: the event finished or
-- was withdrawn, or it started and the invitation needed an open roster
-- (a team invitation, or registration locked at the start). expired_before
-- trails now by a grace period, so a quickly rescheduled event keeps them.
DELETE FROM event_participants
WHERE (event_id, user_id) IN (
    SELECT p.event_id, p.user_id
    FROM event_participants p
    JOIN events e ON e.id = p.event_id
    WHERE p.invited
      AND p.status = 1
      AND e.lifecycle_configured
      AND (e.withdraw_at < sqlc.arg(expired_before)::timestamptz
        OR LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(expired_before)::timestamptz
        OR (e.start_at < sqlc.arg(expired_before)::timestamptz AND (p.invited_to_team OR e.join_policy = 0)))
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE OF p SKIP LOCKED);

-- name: PurgeUnconfirmedAccounts :one
-- Accounts whose registration was never finished (invited or self sign-up)
-- whose creation and last (re)sent invitation are both before
-- created_before; see purge_unconfirmed_accounts (migration 0100).
SELECT purge_unconfirmed_accounts(sqlc.arg(created_before)::timestamptz, sqlc.arg(now_at)::timestamptz, sqlc.arg(batch_size)::integer)::bigint AS purged;

-- name: ListLabTelemetryErasures :many
-- Accounts erased from the lab telemetry that have not settled yet.
SELECT user_id, client_name, pseudonym, event_ids, queued_at, archives_scrubbed_at
FROM lab_telemetry_erasures
ORDER BY queued_at, user_id
LIMIT sqlc.arg(batch_size);

-- name: ScrubRawLabObservations :execrows
-- Rewrites up to batch_size raw observations of the events that still carry the client name.
UPDATE event_lab_observations
SET payload = replace(payload::text, sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text)::jsonb
WHERE id IN (SELECT o.id
             FROM event_lab_observations o
             WHERE o.event_id = ANY (sqlc.arg(event_ids)::uuid[])
               AND o.payload::text LIKE '%' || sqlc.arg(client_name)::text || '%'
             LIMIT sqlc.arg(batch_size));

-- name: ScrubLabMonitoringCurrent :execrows
UPDATE lab_monitoring_current
SET payload = replace(payload::text, sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text)::jsonb
WHERE event_id = ANY (sqlc.arg(event_ids)::uuid[])
  AND payload::text LIKE '%' || sqlc.arg(client_name)::text || '%';

-- name: RenameErasedLabClient :exec
-- Gives the rollups' rows that were built from telemetry still carrying the client name the pseudonym. A row whose
-- pseudonymous twin exists already is dropped instead of renamed (the primary keys would collide).
WITH minutes_renamed AS (
    UPDATE event_vpn_client_minutes m SET client_name = sqlc.arg(pseudonym)::text
    WHERE m.client_name = sqlc.arg(client_name)::text
      AND NOT EXISTS (SELECT 1 FROM event_vpn_client_minutes t
                      WHERE t.event_id = m.event_id AND t.team_id = m.team_id AND t.lab_group_name = m.lab_group_name
                        AND t.client_name = sqlc.arg(pseudonym)::text AND t.bucket_at = m.bucket_at)
), minutes_dropped AS (
    DELETE FROM event_vpn_client_minutes m
    WHERE m.client_name = sqlc.arg(client_name)::text
      AND EXISTS (SELECT 1 FROM event_vpn_client_minutes t
                  WHERE t.event_id = m.event_id AND t.team_id = m.team_id AND t.lab_group_name = m.lab_group_name
                    AND t.client_name = sqlc.arg(pseudonym)::text AND t.bucket_at = m.bucket_at)
), transitions_renamed AS (
    UPDATE event_lab_transitions SET client_name = sqlc.arg(pseudonym)::text WHERE client_name = sqlc.arg(client_name)::text
), states_renamed AS (
    UPDATE event_lab_journal_states s SET name = sqlc.arg(pseudonym)::text
    WHERE s.kind = 'client' AND s.name = sqlc.arg(client_name)::text
      AND NOT EXISTS (SELECT 1 FROM event_lab_journal_states t
                      WHERE t.event_id = s.event_id AND t.team_id = s.team_id AND t.lab_group_name = s.lab_group_name
                        AND t.kind = 'client' AND t.lab_name = s.lab_name AND t.name = sqlc.arg(pseudonym)::text)
)
DELETE FROM event_lab_journal_states s
WHERE s.kind = 'client' AND s.name = sqlc.arg(client_name)::text
  AND EXISTS (SELECT 1 FROM event_lab_journal_states t
              WHERE t.event_id = s.event_id AND t.team_id = s.team_id AND t.lab_group_name = s.lab_group_name
                AND t.kind = 'client' AND t.lab_name = s.lab_name AND t.name = sqlc.arg(pseudonym)::text);

-- name: ListLabArchiveChunksForScrub :many
-- The archive chunks of an event created after created_after, after the given id, a page at a time.
SELECT id, codec, data
FROM event_lab_observation_archives
WHERE event_id = sqlc.arg(event_id)
  AND created_at > sqlc.arg(created_after)::timestamptz
  AND id > sqlc.arg(after_id)::uuid
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: UpdateLabArchiveChunkData :exec
UPDATE event_lab_observation_archives SET data = sqlc.arg(data) WHERE id = sqlc.arg(id);

-- name: MarkLabTelemetryErasuresScrubbed :exec
UPDATE lab_telemetry_erasures SET archives_scrubbed_at = sqlc.arg(scrubbed_at)::timestamptz
WHERE user_id = ANY (sqlc.arg(user_ids)::uuid[]);

-- name: DeleteSettledLabTelemetryErasures :execrows
-- An entry settles a day after the account was purged, once the archive passed every raw row of its events that
-- arrived before that: nothing that still carries the real name can be archived after it goes.
DELETE FROM lab_telemetry_erasures e
WHERE e.queued_at < sqlc.arg(settled_before)::timestamptz
  AND NOT EXISTS (SELECT 1
                  FROM event_lab_observations o
                  LEFT JOIN event_analytics_rollups r ON r.event_id = o.event_id
                  WHERE o.event_id = ANY (e.event_ids)
                    AND o.received_at < e.queued_at + interval '1 day'
                    AND (r.lab_archive_cursor_id IS NULL
                         OR (o.received_at, o.id) > (r.lab_archive_cursor_received_at, r.lab_archive_cursor_id)));
