CREATE TABLE event_preview_settings (
 event_id uuid PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 selections jsonb CHECK (selections IS NULL OR jsonb_typeof(selections) = 'array'),
 group_name text CHECK (group_name IS NULL OR char_length(group_name) BETWEEN 1 AND 100),
 updated_at timestamptz NOT NULL
);
CREATE TABLE event_preview_runs (
 id uuid PRIMARY KEY,
 event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 selection_id uuid NOT NULL,
 deploy_id uuid,
 state jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 UNIQUE(event_id,user_id,selection_id)
);
CREATE INDEX event_preview_runs_expiry_idx ON event_preview_runs(expires_at);

ALTER TABLE exercise_test_deployments ADD COLUMN event_id uuid;
ALTER TABLE resource_test_lab_holds ADD COLUMN event_id uuid;
ALTER TABLE resource_test_lab_holds ADD COLUMN group_name text NOT NULL DEFAULT '';
ALTER TABLE resource_test_lab_holds DROP CONSTRAINT resource_test_lab_holds_via_check;
ALTER TABLE resource_test_lab_holds ADD CONSTRAINT resource_test_lab_holds_via_check CHECK (via IN ('pool','free','booking','event_preview','event_preview_group'));
