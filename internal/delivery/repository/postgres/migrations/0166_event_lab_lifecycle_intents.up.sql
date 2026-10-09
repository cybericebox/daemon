-- Private durable identity/preparation bookkeeping; no new operator powers.
ALTER TABLE event_team_labs ADD COLUMN create_evidence jsonb,
 ADD COLUMN runtime_stage_id uuid,
 ADD COLUMN runtime_stage_known boolean NOT NULL DEFAULT false;
ALTER TABLE event_stage_lab_runtime_memberships
 ADD COLUMN selected_revision bigint NOT NULL DEFAULT 0 CHECK(selected_revision>=0),
 ADD COLUMN consumed_revision bigint,
 ADD COLUMN consumed_at timestamptz;
