ALTER TABLE event_stages ADD COLUMN lab_retention_minutes integer CHECK(lab_retention_minutes BETWEEN 0 AND 10080);
ALTER TABLE event_team_labs
 ADD COLUMN retirement_stop_target jsonb,
 ADD COLUMN retirement_state text NOT NULL DEFAULT 'Unknown',
 ADD COLUMN retirement_observed_at timestamptz,
 ADD COLUMN retirement_error text NOT NULL DEFAULT '';
ALTER TABLE event_team_group_allocations
 ADD COLUMN retirement_stop_target jsonb,
 ADD COLUMN retirement_state text NOT NULL DEFAULT 'Unknown',
 ADD COLUMN retirement_observed_at timestamptz,
 ADD COLUMN retirement_error text NOT NULL DEFAULT '';
CREATE TABLE event_lab_retention_pins (
 lab_id uuid NOT NULL REFERENCES event_team_labs(id),
 stage_id uuid NOT NULL,
 generation integer NOT NULL,
 needed_from timestamptz NOT NULL,
 needed_until timestamptz NOT NULL,
 PRIMARY KEY(lab_id,stage_id,generation),
 CHECK(needed_until>=needed_from)
);
CREATE TABLE event_lab_group_retention_pins (
 event_team_id uuid NOT NULL REFERENCES event_team_group_allocations(event_team_id),
 stage_id uuid NOT NULL,
 needed_from timestamptz NOT NULL,
 needed_until timestamptz NOT NULL,
 PRIMARY KEY(event_team_id,stage_id),
 CHECK(needed_until>=needed_from)
);
-- Explicit internal runtime selections, never inferred by exercise/name equality.
CREATE TABLE event_stage_lab_runtime_memberships (
 stage_id uuid NOT NULL,
 lab_id uuid NOT NULL REFERENCES event_team_labs(id),
 generation integer NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(stage_id,lab_id,generation)
);
CREATE TABLE event_lab_generations (
 lab_id uuid NOT NULL REFERENCES event_team_labs(id),
 generation integer NOT NULL,
 lab_group_name text NOT NULL,
 lab_name text NOT NULL,
 agent_uid text NOT NULL,
 operation_id uuid NOT NULL,
 lifecycle_revision bigint NOT NULL,
 retention_until timestamptz,
 protected_until timestamptz,
 actual_state text NOT NULL,
 allocation jsonb NOT NULL,
 PRIMARY KEY(lab_id,generation),
 UNIQUE(lab_group_name,lab_name)
);
