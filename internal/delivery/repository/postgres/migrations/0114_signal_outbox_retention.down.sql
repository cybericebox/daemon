DROP FUNCTION IF EXISTS signal_payload_anonymized(jsonb, text[]);
DROP INDEX IF EXISTS signal_outbox_finished_idx;
DROP INDEX IF EXISTS signal_outbox_actor_user_idx;
DROP INDEX IF EXISTS signal_outbox_subject_user_idx;
