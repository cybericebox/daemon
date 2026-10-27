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
-- Raw rows past the participants period; only the ones a chunk of the archive really holds (event_lab_archived_rows,
-- not just 'behind the archive cursor'), so nothing is lost before the archive.
DELETE FROM event_lab_observations
WHERE id IN (SELECT o.id
             FROM event_lab_observations o
             JOIN event_analytics_rollups r ON r.event_id = o.event_id
             WHERE o.observed_at < sqlc.arg(observed_before)
               AND (r.lab_telemetry_hold_until IS NULL OR r.lab_telemetry_hold_until <= now())
               AND EXISTS (SELECT 1 FROM event_lab_archived_rows ar WHERE ar.row_id = o.id)
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF o SKIP LOCKED);

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
               AND EXISTS (SELECT 1 FROM event_lab_archived_rows ar WHERE ar.row_id = o.id)
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

-- Derived lab telemetry has two periods, both counted from the event's finish (the manual one if earlier). The
-- data about containers, labs and teams (the minute tables, the transitions and states that name no client) goes
-- LabTelemetry after it; the data about people (client minutes, client_* transitions and states, and the raw
-- archive with its clients and IPs) goes after the shorter LabTelemetryParticipants. An event with no end
-- (permanent, not finished) has nothing finished: its rows go by their own time (the minute, the transition, the
-- end of the archived hour), a rolling window. closed_before is the cutoff (now minus the period) of the data in
-- question.

-- name: PurgeEventLabUsageMinutes :execrows
DELETE FROM event_lab_usage_minutes
WHERE ctid IN (SELECT m.ctid
               FROM event_lab_usage_minutes m
               JOIN events e ON e.id = m.event_id
               WHERE COALESCE(LEAST(e.manual_finished_at, e.finish_at), m.bucket_at) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF m SKIP LOCKED);

-- name: PurgeEventLabMinutes :execrows
DELETE FROM event_lab_minutes
WHERE ctid IN (SELECT m.ctid
               FROM event_lab_minutes m
               JOIN events e ON e.id = m.event_id
               WHERE COALESCE(LEAST(e.manual_finished_at, e.finish_at), m.bucket_at) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF m SKIP LOCKED);

-- name: PurgeEventVPNClientMinutes :execrows
DELETE FROM event_vpn_client_minutes
WHERE ctid IN (SELECT m.ctid
               FROM event_vpn_client_minutes m
               JOIN events e ON e.id = m.event_id
               WHERE COALESCE(LEAST(e.manual_finished_at, e.finish_at), m.bucket_at) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF m SKIP LOCKED);

-- name: PurgeEventLabTransitions :execrows
-- with_clients selects the transitions of a client (the participants period) or the others.
DELETE FROM event_lab_transitions
WHERE id IN (SELECT t.id
             FROM event_lab_transitions t
             JOIN events e ON e.id = t.event_id
             WHERE (t.client_name IS NOT NULL) = sqlc.arg(with_clients)::boolean
               AND COALESCE(LEAST(e.manual_finished_at, e.finish_at), t.at) < sqlc.arg(closed_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF t SKIP LOCKED);

-- name: PurgeEventLabJournalStates :execrows
-- The remembered states have no time of their own: they go with a finished event only. with_clients selects the
-- states of clients (the participants period) or the others.
DELETE FROM event_lab_journal_states
WHERE ctid IN (SELECT s.ctid
               FROM event_lab_journal_states s
               JOIN events e ON e.id = s.event_id
               WHERE (s.kind = 'client') = sqlc.arg(with_clients)::boolean
                 AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(closed_before)::timestamptz
               LIMIT sqlc.arg(batch_size)
               FOR UPDATE OF s SKIP LOCKED);

-- name: PurgeEventLabObservationArchives :execrows
-- The raw archive holds clients and IPs: the participants period.
DELETE FROM event_lab_observation_archives
WHERE id IN (SELECT a.id
             FROM event_lab_observation_archives a
             JOIN events e ON e.id = a.event_id
             WHERE COALESCE(LEAST(e.manual_finished_at, e.finish_at), a.period_end) < sqlc.arg(closed_before)::timestamptz
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
    -- traffic stay). This statement only queues the account with its pseudonym and the events it took part in; the
    -- retention job renames and rewrites the telemetry right after, in batches (the queue entry lives for a day and
    -- every run rescans, so nothing archived meanwhile keeps the name).
    INSERT INTO lab_telemetry_erasures (user_id, client_name, pseudonym, event_ids)
    SELECT b.id, lab_client_name(b.id), 'deleted-' || gen_random_uuid()::text,
           ARRAY(SELECT x.event_id
                 FROM (SELECT p.event_id FROM event_participants p WHERE p.user_id = b.id
                       UNION
                       SELECT m.event_id FROM event_vpn_client_minutes m WHERE m.client_name = lab_client_name(b.id)
                       UNION
                       SELECT v.event_id FROM event_vpn_sessions v WHERE v.user_id = b.id) x)
    FROM batch b
    ON CONFLICT (user_id) DO UPDATE
        SET queued_at = now(),
            event_ids = ARRAY(SELECT DISTINCT x FROM unnest(lab_telemetry_erasures.event_ids || EXCLUDED.event_ids) x)
    RETURNING user_id
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
-- Accounts erased from the lab telemetry that have not settled yet, a page at a time by (queued_at, user_id).
SELECT user_id, client_name, pseudonym, event_ids, queued_at, erase_ip, ip_windows
FROM lab_telemetry_erasures
WHERE (queued_at, user_id) > (sqlc.arg(after_queued_at)::timestamptz, sqlc.arg(after_user_id)::uuid)
ORDER BY queued_at, user_id
LIMIT sqlc.arg(batch_size);

-- name: ListSettledLabTelemetryErasures :many
-- The entries that may be closed after this pass. Chosen before the pass scrubs: anything that could still carry the
-- real name was archived (or arrived) by then and the pass rewrites it. An entry settles a day after it was queued,
-- once the archive passed every raw row of its events that arrived within that day; one that never can (the event
-- has no rollup row, the rows are gone or stuck) is closed after force_before all the same.
SELECT e.user_id, e.queued_at
FROM lab_telemetry_erasures e
WHERE e.queued_at < sqlc.arg(force_before)::timestamptz
   OR (e.queued_at < sqlc.arg(settled_before)::timestamptz
       AND NOT EXISTS (SELECT 1
                       FROM event_lab_observations o
                       LEFT JOIN event_analytics_rollups r ON r.event_id = o.event_id
                       WHERE o.event_id = ANY (e.event_ids)
                         AND o.received_at < e.queued_at + interval '1 day'
                         AND (r.lab_archive_cursor_id IS NULL
                              OR (o.received_at, o.id) > (r.lab_archive_cursor_received_at, r.lab_archive_cursor_id))));

-- name: CountLabTelemetryErasuresQueuedBefore :one
SELECT count(*) FROM lab_telemetry_erasures WHERE queued_at < sqlc.arg(queued_before)::timestamptz;

-- name: DeleteLabTelemetryErasures :exec
-- Closes the entries chosen as settled, by (user_id, queued_at): an entry queued again meanwhile (a new time) stays.
DELETE FROM lab_telemetry_erasures e
USING jsonb_to_recordset(sqlc.arg(entries)::jsonb) AS settled(user_id uuid, queued_at timestamptz)
WHERE e.user_id = settled.user_id AND e.queued_at = settled.queued_at;

-- name: QueueLabTelemetryErasure :one
-- Erases one person on request (an objection): the same as the purge of a deleted account does, for any user id.
-- Queues the person with a random pseudonym (the existing one when queued already, with the day starting afresh),
-- only queues; the scrub that follows renames the rollup rows, rewrites the raw rows, the current state and the
-- archive, and first collects the person's IPs. Returns the queued entry.
WITH person AS (
    SELECT sqlc.arg(user_id)::uuid AS id, lab_client_name(sqlc.arg(user_id)::uuid) AS client_name
), queued AS (
    INSERT INTO lab_telemetry_erasures (user_id, client_name, pseudonym, event_ids, erase_ip)
    SELECT p.id, p.client_name, 'deleted-' || gen_random_uuid()::text,
           ARRAY(SELECT x.event_id
                 FROM (SELECT pa.event_id FROM event_participants pa WHERE pa.user_id = p.id
                       UNION
                       SELECT m.event_id FROM event_vpn_client_minutes m WHERE m.client_name = p.client_name
                       UNION
                       SELECT v.event_id FROM event_vpn_sessions v WHERE v.user_id = p.id) x),
           true
    FROM person p
    ON CONFLICT (user_id) DO UPDATE
        SET queued_at = now(),
            event_ids = ARRAY(SELECT DISTINCT x FROM unnest(lab_telemetry_erasures.event_ids || EXCLUDED.event_ids) x),
            erase_ip = lab_telemetry_erasures.erase_ip OR EXCLUDED.erase_ip
    RETURNING user_id, client_name, pseudonym, event_ids, queued_at, erase_ip, ip_windows
)
SELECT user_id, client_name, pseudonym, event_ids, queued_at, erase_ip, ip_windows FROM queued;

-- name: ScrubRawLabObservations :execrows
-- Rewrites up to batch_size raw observations of the events that still carry the client name or an IP window of the
-- client (see lab_apply_ip_windows): the name becomes the pseudonym, the IPs go inside the windows. names are the
-- person's names (the real one and the pseudonym).
UPDATE event_lab_observations
SET payload = lab_apply_ip_windows(replace(payload::text, sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text)::jsonb,
                                   event_team_id, lab_group_name, observed_at, sqlc.arg(windows)::jsonb,
                                   ARRAY [sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text])
WHERE id IN (SELECT o.id
             FROM event_lab_observations o
             WHERE o.event_id = ANY (sqlc.arg(event_ids)::uuid[])
               AND (o.payload::text LIKE '%' || sqlc.arg(client_name)::text || '%'
                    OR lab_apply_ip_windows(o.payload, o.event_team_id, o.lab_group_name, o.observed_at, sqlc.arg(windows)::jsonb,
                                            ARRAY [sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text]) <> o.payload)
             LIMIT sqlc.arg(batch_size));

-- name: ScrubLabMonitoringCurrent :execrows
UPDATE lab_monitoring_current
SET payload = lab_apply_ip_windows(replace(payload::text, sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text)::jsonb,
                                   event_team_id, lab_group_name, observed_at, sqlc.arg(windows)::jsonb,
                                   ARRAY [sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text])
WHERE event_id = ANY (sqlc.arg(event_ids)::uuid[])
  AND (payload::text LIKE '%' || sqlc.arg(client_name)::text || '%'
       OR lab_apply_ip_windows(payload, event_team_id, lab_group_name, observed_at, sqlc.arg(windows)::jsonb,
                               ARRAY [sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text]) <> payload);

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
-- The archive chunks of an event after the given id, a page at a time.
SELECT id, codec, data
FROM event_lab_observation_archives
WHERE event_id = sqlc.arg(event_id)
  AND id > sqlc.arg(after_id)::uuid
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: GetLabArchiveChunkForScrub :one
SELECT id, codec, data FROM event_lab_observation_archives WHERE id = sqlc.arg(id);

-- name: UpdateLabArchiveChunkData :execrows
-- Replaces the data only if nobody changed it since it was read (another scrub); the caller reads again otherwise.
UPDATE event_lab_observation_archives SET data = sqlc.arg(data)
WHERE id = sqlc.arg(id) AND data = sqlc.arg(old_data);

-- name: CollectLabClientIPWindows :many
-- When, in which lab group, the live telemetry (raw rows and current state) of the events gave which IP to the client
-- (under its real name or its pseudonym): the first and the last observation.
SELECT x.team_id::uuid AS team_id, x.lab_group_name::text AS lab_group_name, x.ip::text AS ip,
       min(x.observed_at)::timestamptz AS first_at, max(x.observed_at)::timestamptz AS last_at
FROM (SELECT o.event_team_id AS team_id, o.lab_group_name, o.observed_at, c -> 'status' ->> 'assignedIp' AS ip, c ->> 'name' AS name
      FROM event_lab_observations o
      CROSS JOIN LATERAL jsonb_array_elements(
              CASE WHEN jsonb_typeof(o.payload -> 'clients') = 'array' THEN o.payload -> 'clients' ELSE '[]'::jsonb END) c
      WHERE o.event_id = ANY (sqlc.arg(event_ids)::uuid[])
        AND (o.payload::text LIKE '%' || sqlc.arg(client_name)::text || '%' OR o.payload::text LIKE '%' || sqlc.arg(pseudonym)::text || '%')
      UNION ALL
      SELECT m.event_team_id, m.lab_group_name, m.observed_at, c -> 'status' ->> 'assignedIp', c ->> 'name'
      FROM lab_monitoring_current m
      CROSS JOIN LATERAL jsonb_array_elements(
              CASE WHEN jsonb_typeof(m.payload -> 'clients') = 'array' THEN m.payload -> 'clients' ELSE '[]'::jsonb END) c
      WHERE m.event_id = ANY (sqlc.arg(event_ids)::uuid[])) x
WHERE x.name IN (sqlc.arg(client_name)::text, sqlc.arg(pseudonym)::text)
  AND COALESCE(x.ip, '') <> ''
GROUP BY x.team_id, x.lab_group_name, x.ip;

-- name: SetLabTelemetryErasureWindows :exec
UPDATE lab_telemetry_erasures SET ip_windows = sqlc.arg(windows)::jsonb WHERE user_id = sqlc.arg(user_id);

-- name: ListLateLabObservations :many
-- Raw rows of one event that no archive chunk holds although the archive cursor is past them (committed late), older
-- than received_before; up to row_limit, with the id and the line the archive keeps.
WITH late AS (
    SELECT o.id, o.event_id, o.received_at, o.event_team_id, o.lab_group_name, o.observed_at
    FROM event_lab_observations o
    JOIN event_analytics_rollups r ON r.event_id = o.event_id
    WHERE o.received_at < sqlc.arg(received_before)::timestamptz
      AND r.lab_archive_cursor_id IS NOT NULL
      AND (o.received_at, o.id) <= (r.lab_archive_cursor_received_at, r.lab_archive_cursor_id)
      AND NOT EXISTS (SELECT 1 FROM event_lab_archived_rows ar WHERE ar.row_id = o.id)
), one_event AS (
    SELECT l.event_id FROM late l ORDER BY l.received_at, l.id LIMIT 1
)
SELECT o.id, o.event_id, o.received_at, to_jsonb(o)::text AS line
FROM event_lab_observations o
WHERE o.id IN (SELECT l.id FROM late l JOIN one_event e ON e.event_id = l.event_id ORDER BY l.received_at, l.id LIMIT sqlc.arg(row_limit))
ORDER BY o.received_at, o.id;

-- name: SaveLateLabObservationArchive :exec
-- A chunk of late rows: stored with its row ids, the archive cursor stays where it is.
WITH chunk AS (
    INSERT INTO event_lab_observation_archives (id, event_id, period_start, period_end, row_count,
                                                first_received_at, first_id, last_received_at, last_id, codec, data)
    VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(period_start), sqlc.arg(period_end), sqlc.arg(row_count),
            sqlc.arg(first_received_at), sqlc.arg(first_id), sqlc.arg(last_received_at), sqlc.arg(last_id),
            sqlc.arg(codec), sqlc.arg(data))
    ON CONFLICT (event_id, last_received_at, last_id) DO NOTHING
    RETURNING id
)
INSERT INTO event_lab_archived_rows (row_id, chunk_id)
SELECT unnest(sqlc.arg(row_ids)::uuid[]), c.id FROM chunk c
ON CONFLICT DO NOTHING;

-- name: CountStaleLabObservations :one
-- Raw rows past the period that are still there.
SELECT count(*) FROM event_lab_observations WHERE observed_at < sqlc.arg(observed_before)::timestamptz;

-- name: DeleteLabArchiveChunk :exec
-- Drops an archive chunk that cannot be read (its rows go with it from event_lab_archived_rows, so the raw rows it held
-- count as unarchived again).
DELETE FROM event_lab_observation_archives WHERE id = sqlc.arg(id);
