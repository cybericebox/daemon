DROP TRIGGER IF EXISTS exercise_test_deploy_runs_del ON exercise_test_deployments;
DROP TRIGGER IF EXISTS exercise_test_deploy_runs_ins ON exercise_test_deployments;
DROP FUNCTION IF EXISTS exercise_test_deploy_runs_sync();
DROP TABLE IF EXISTS exercise_test_deploy_runs;
