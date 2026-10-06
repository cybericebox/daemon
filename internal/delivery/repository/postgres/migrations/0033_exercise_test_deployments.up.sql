CREATE TABLE exercise_test_deployments (
    id uuid PRIMARY KEY,
    group_name text NOT NULL UNIQUE,
    version_id uuid NOT NULL REFERENCES exercise_versions(id) ON DELETE RESTRICT,
    variant_id uuid NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX exercise_test_deployments_owner_idx ON exercise_test_deployments (created_by);
CREATE INDEX exercise_test_deployments_expiry_idx ON exercise_test_deployments (expires_at);
