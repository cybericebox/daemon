-- Durable outcomes share the catalog transaction. An older binary's unconditional
-- outcome UPDATE must not erase them while new and old workers overlap.
LOCK TABLE exercise_import_jobs IN ACCESS EXCLUSIVE MODE;
ALTER TABLE exercise_import_jobs
    ADD COLUMN generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    ADD COLUMN options jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN plan_hash text NOT NULL DEFAULT '',
    ADD COLUMN results jsonb NOT NULL DEFAULT '[]'::jsonb;
CREATE TRIGGER exercise_import_writer BEFORE INSERT OR UPDATE OR DELETE ON exercise_import_jobs
FOR EACH ROW EXECUTE FUNCTION require_versioned_catalog_writer();
