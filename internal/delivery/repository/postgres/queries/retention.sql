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
DELETE FROM event_lab_observations
WHERE id IN (SELECT o.id
             FROM event_lab_observations o
             WHERE o.observed_at < sqlc.arg(observed_before)
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE SKIP LOCKED);

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
