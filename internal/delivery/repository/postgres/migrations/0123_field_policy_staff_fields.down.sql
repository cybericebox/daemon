DROP TABLE event_staff_field_changes;
ALTER TABLE event_teams DROP COLUMN fields_missing;
ALTER TABLE event_participants DROP COLUMN fields_missing;
ALTER TABLE event_team_field_configs DROP COLUMN block_submissions, DROP COLUMN require_existing;
ALTER TABLE event_form_versions DROP COLUMN block_submissions, DROP COLUMN require_existing;
