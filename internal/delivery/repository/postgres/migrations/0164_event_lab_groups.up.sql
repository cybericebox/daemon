-- Lifecycle shares the immutable per-team allocation envelope from0162.
ALTER TABLE event_team_group_allocations
 ADD COLUMN agent_uid text NOT NULL DEFAULT '',
 ADD COLUMN agent_generation bigint NOT NULL DEFAULT 0 CHECK(agent_generation>=0),
 ADD COLUMN desired_revision bigint NOT NULL DEFAULT 1 CHECK(desired_revision>0),
 ADD COLUMN observed_revision bigint NOT NULL DEFAULT 0 CHECK(observed_revision>=0),
 ADD COLUMN operation_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
 ADD COLUMN desired_state text NOT NULL DEFAULT 'Running' CHECK(desired_state IN ('Running','Stopped','Deleted')),
 ADD COLUMN actual_state text NOT NULL DEFAULT 'Unknown',
 ADD COLUMN ready boolean NOT NULL DEFAULT false,
 ADD COLUMN observed_at timestamptz,
 ADD COLUMN allocation jsonb NOT NULL DEFAULT '{}',
 ADD COLUMN access_fenced boolean NOT NULL DEFAULT false,
 ADD COLUMN failure_code text NOT NULL DEFAULT '',
 ADD COLUMN failure_message text NOT NULL DEFAULT '',
 ADD COLUMN pending_starts integer NOT NULL DEFAULT 0 CHECK(pending_starts>=0),
 ADD COLUMN retention_until timestamptz,
 ADD COLUMN protected_until timestamptz,
 ADD COLUMN next_attempt_at timestamptz,
 ADD COLUMN updated_at timestamptz;
UPDATE event_team_group_allocations SET updated_at=created_at,next_attempt_at=created_at;
ALTER TABLE event_team_group_allocations ALTER COLUMN updated_at SET NOT NULL,ALTER COLUMN next_attempt_at SET NOT NULL;
ALTER TABLE event_team_group_allocations ALTER COLUMN operation_id DROP DEFAULT;
