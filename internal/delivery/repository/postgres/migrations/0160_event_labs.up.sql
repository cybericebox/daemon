-- Durable lifecycle and objective pins deliberately have no cascading scope FKs.
CREATE TABLE event_team_labs (
 id uuid PRIMARY KEY,
 event_id uuid NOT NULL,
 event_team_id uuid NOT NULL,
 event_exercise_id uuid NOT NULL,
 variant_index integer NOT NULL CHECK (variant_index >= 0),
 generation integer NOT NULL CHECK (generation >= 0),
 lab_group_name text NOT NULL CHECK (lab_group_name ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' AND length(lab_group_name) <= 63),
 lab_name text NOT NULL CHECK (lab_name ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' AND length(lab_name) <= 63),
 agent_uid text NOT NULL DEFAULT '',
 agent_generation bigint NOT NULL DEFAULT 0 CHECK (agent_generation >= 0),
 desired_revision bigint NOT NULL DEFAULT 1 CHECK (desired_revision > 0),
 observed_revision bigint NOT NULL DEFAULT 0 CHECK (observed_revision >= 0 AND observed_revision <= desired_revision),
 operation_id uuid NOT NULL,
 desired_state text NOT NULL DEFAULT 'Running' CHECK (desired_state IN ('Running','Stopped','Deleted')),
 actual_state text NOT NULL DEFAULT 'Unknown',
 runtime_ready boolean NOT NULL DEFAULT false,
 close_reason text CHECK (close_reason IN ('solved','manual','stage','event')),
 logical_closed_at timestamptz,
 snapshot_mode text NOT NULL CHECK (snapshot_mode IN ('skip','required')),
 snapshot_state text NOT NULL DEFAULT 'Unknown',
 retention_until timestamptz,
 protected_until timestamptz,
 actual_stopped_at timestamptz,
 observed_at timestamptz,
 objective_count integer NOT NULL CHECK (objective_count > 0),
 materialized boolean NOT NULL DEFAULT false,
 allocation jsonb NOT NULL DEFAULT '{}',
 failure_code text NOT NULL DEFAULT '',
 failure_message text NOT NULL DEFAULT '',
 access_fenced boolean NOT NULL DEFAULT false,
 access_fenced_at timestamptz,
 access_fence_vpn_boot_id text NOT NULL DEFAULT '',
 next_attempt_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE (lab_group_name, lab_name),
 UNIQUE (id, event_team_id),
 UNIQUE (event_team_id,event_exercise_id,generation)
);
CREATE TABLE event_lab_objectives (
 lab_id uuid NOT NULL REFERENCES event_team_labs(id),
 event_challenge_id uuid NOT NULL,
 PRIMARY KEY (lab_id,event_challenge_id)
);
ALTER TABLE lab_bindings ADD COLUMN lab_id uuid;
ALTER TABLE lab_bindings ADD CONSTRAINT lab_bindings_shared_lab_fk
 FOREIGN KEY (lab_id,event_team_id) REFERENCES event_team_labs(id,event_team_id);
CREATE INDEX event_team_labs_dirty_idx ON event_team_labs(next_attempt_at,id);
ALTER TABLE event_configs ADD COLUMN lab_policy jsonb;
ALTER TABLE event_configs ADD CONSTRAINT event_configs_lab_policy_check CHECK (
 lab_policy IS NULL OR COALESCE((
  jsonb_typeof(lab_policy)='object' AND lab_policy ?& ARRAY['SnapshotMode','MaxActiveLabsPerTeam','RetentionMinutes']
  AND lab_policy->>'SnapshotMode' IN ('skip','required')
  AND jsonb_typeof(lab_policy->'RetentionMinutes')='number'
  AND (lab_policy->>'RetentionMinutes')::integer BETWEEN 0 AND 10080
  AND (jsonb_typeof(lab_policy->'MaxActiveLabsPerTeam')='null' OR (jsonb_typeof(lab_policy->'MaxActiveLabsPerTeam')='number' AND (lab_policy->>'MaxActiveLabsPerTeam')::integer BETWEEN 1 AND 1000))
 ),false)
);
