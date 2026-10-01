-- The concrete object a state-changing admin action addressed (route ids).
-- Empty for actions whose route template already says everything.
ALTER TABLE admin_audit_log ADD COLUMN target text NOT NULL DEFAULT '';
