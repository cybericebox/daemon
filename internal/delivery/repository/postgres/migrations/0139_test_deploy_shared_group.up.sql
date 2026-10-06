-- One lab group per author for the catalog test labs: every test deploy of a user is a Lab in
-- the same group (group_name = 'tu-<user id>'), each with its own subnet, and the author has one
-- VPN config for all of them. The table must be empty when this runs (the running test deploys
-- are ended first): lab_name has no default, and the per-deploy VPN configs of the old layout
-- (scope 'test' with a deploy id as scope_ref) are dropped.
DELETE FROM user_vpn_configs WHERE scope = 'test' AND scope_ref IS NOT NULL;
ALTER TABLE exercise_test_deployments DROP CONSTRAINT exercise_test_deployments_group_name_key;
CREATE INDEX exercise_test_deployments_group_idx ON exercise_test_deployments (group_name);
ALTER TABLE exercise_test_deployments ADD COLUMN lab_name text NOT NULL;
