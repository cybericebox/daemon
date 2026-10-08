ALTER TABLE event_stage_lab_runtime_memberships DROP COLUMN consumed_at,DROP COLUMN consumed_revision,DROP COLUMN selected_revision;
ALTER TABLE event_team_labs DROP COLUMN runtime_stage_known,DROP COLUMN runtime_stage_id,DROP COLUMN create_evidence;
