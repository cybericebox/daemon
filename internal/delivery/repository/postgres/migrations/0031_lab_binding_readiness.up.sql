ALTER TABLE lab_bindings
    ADD COLUMN readiness smallint NOT NULL DEFAULT 0
        CHECK (readiness IN (0, 1, 2));
