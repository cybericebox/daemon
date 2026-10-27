-- A failed lab binding is retried by the stand engine with a backoff, and a pending one that waits says why.
-- failed_at / retry_attempts drive the backoff; wait_reason / wait_since name what a pending lab waits for
-- (a machine code, empty when it does not wait) and since when, so a lab stuck for long raises the alarm.
ALTER TABLE lab_bindings
    ADD COLUMN failed_at      timestamptz,
    ADD COLUMN retry_attempts integer     NOT NULL DEFAULT 0 CHECK (retry_attempts >= 0),
    ADD COLUMN wait_reason    text        NOT NULL DEFAULT '',
    ADD COLUMN wait_since     timestamptz;
