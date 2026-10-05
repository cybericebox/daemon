-- Restore the unfiltered view before the practice column goes.
CREATE OR REPLACE VIEW effective_challenge_attempts AS
SELECT ca.id,
       ca.event_id,
       ca.event_team_id,
       ca.team_challenge_id,
       ca.user_id,
       ca.answer,
       ca.correct AS automatic_correct,
       ca.received_at,
       ca.created_at,
       COALESCE(latest.id, '00000000-0000-0000-0000-000000000000'::uuid) AS decision_id,
       COALESCE(latest.decision, 0) AS decision,
       COALESCE(latest.reason, '')::text AS decision_reason,
       COALESCE(latest.decided_by, '00000000-0000-0000-0000-000000000000'::uuid) AS decided_by,
       COALESCE(latest.decided_at, 'epoch'::timestamptz) AS decided_at,
       CASE COALESCE(latest.decision, 0)
           WHEN 1 THEN TRUE
           WHEN 2 THEN FALSE
           ELSE ca.correct
       END::boolean AS effective_correct
FROM challenge_attempts ca
LEFT JOIN LATERAL (
    SELECT cad.id, cad.decision, cad.reason, cad.decided_by, cad.decided_at
    FROM challenge_attempt_decisions cad
    WHERE cad.challenge_attempt_id = ca.id
    ORDER BY cad.decided_at DESC, cad.id DESC
    LIMIT 1
) latest ON TRUE;
DROP VIEW IF EXISTS effective_challenge_attempts_all;

ALTER TABLE event_configs
    ADD COLUMN stand_deploy_lead_minutes integer NOT NULL DEFAULT 30
        CHECK (stand_deploy_lead_minutes BETWEEN 5 AND 1440);

ALTER TABLE event_lab_access_syncs
    DROP COLUMN IF EXISTS applied_stage_epoch;

DROP FUNCTION IF EXISTS event_stage_epoch(uuid, timestamptz);
DROP FUNCTION IF EXISTS event_stage_phase(timestamptz, timestamptz, boolean, timestamptz);

ALTER TABLE event_configs
    DROP CONSTRAINT IF EXISTS event_configs_finish_countdown_mode_check,
    DROP COLUMN IF EXISTS finish_countdown_mode;

ALTER TABLE challenge_attempts
    DROP COLUMN IF EXISTS practice;
DROP TABLE IF EXISTS team_challenge_practice_solves;

DROP INDEX IF EXISTS event_exercises_stage_idx;
ALTER TABLE event_exercises
    DROP CONSTRAINT IF EXISTS event_exercises_stage_fkey,
    DROP COLUMN IF EXISTS stage_id;

DROP TABLE IF EXISTS event_stages;
