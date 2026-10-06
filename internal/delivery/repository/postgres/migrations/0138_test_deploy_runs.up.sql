-- The history of the catalog test labs (exercise test deploys): a row of
-- exercise_test_deployments is removed when the lab is stopped or its lease is
-- cleaned up, so the platform analytics cannot read the lab hours from it. The
-- runs are written by triggers, so every write path is recorded and the deploy
-- code is untouched. ended_at stays NULL while the lab runs.
--
-- Why triggers: the deploy Go code is owned elsewhere and the rows of
-- exercise_test_deployments are deleted on stop and on lease expiry, so a
-- trigger is the one place that sees every insert and delete (0115 does the
-- same for stands). version_id / variant_id let the analytics group by
-- exercise and variant.
CREATE TABLE exercise_test_deploy_runs
(
    deploy_id  uuid PRIMARY KEY,
    group_name text        NOT NULL,
    created_by uuid        NOT NULL,
    version_id uuid        NOT NULL,
    variant_id uuid        NOT NULL,
    started_at timestamptz NOT NULL,
    ended_at   timestamptz
);

CREATE INDEX exercise_test_deploy_runs_started_idx ON exercise_test_deploy_runs (started_at);

CREATE FUNCTION exercise_test_deploy_runs_sync() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO exercise_test_deploy_runs (deploy_id, group_name, created_by, version_id, variant_id, started_at)
        VALUES (NEW.id, NEW.group_name, NEW.created_by, NEW.version_id, NEW.variant_id, NEW.created_at)
        ON CONFLICT (deploy_id) DO NOTHING;
        RETURN NEW;
    END IF;
    UPDATE exercise_test_deploy_runs SET ended_at = now() WHERE deploy_id = OLD.id AND ended_at IS NULL;
    RETURN OLD;
END
$$;

CREATE TRIGGER exercise_test_deploy_runs_ins
    AFTER INSERT
    ON exercise_test_deployments
    FOR EACH ROW
EXECUTE FUNCTION exercise_test_deploy_runs_sync();

CREATE TRIGGER exercise_test_deploy_runs_del
    AFTER DELETE
    ON exercise_test_deployments
    FOR EACH ROW
EXECUTE FUNCTION exercise_test_deploy_runs_sync();

INSERT INTO exercise_test_deploy_runs (deploy_id, group_name, created_by, version_id, variant_id, started_at)
SELECT id, group_name, created_by, version_id, variant_id, created_at
FROM exercise_test_deployments;
