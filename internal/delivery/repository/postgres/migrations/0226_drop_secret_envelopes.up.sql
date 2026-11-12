-- Sealed secrets of signals never had a user: background-job arguments are sealed with the platform cipher
-- (temporal_codes), so the envelope table, its indexes and its per-user purge are gone.
DROP INDEX IF EXISTS secret_envelopes_recipient_idx;
DROP INDEX IF EXISTS secret_envelopes_scope_idx;
DROP TABLE IF EXISTS secret_envelopes;
