-- What an administrative action changed, field by field (before and after). Secrets are cut out before the
-- row is written, so only redaction markers for them are ever stored.
ALTER TABLE admin_audit_log
    ADD COLUMN changes jsonb NOT NULL DEFAULT '[]'::jsonb;
