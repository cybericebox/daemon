ALTER TABLE event_teams DROP CONSTRAINT event_teams_extra_fields_object;
ALTER TABLE event_teams DROP COLUMN extra_fields;
DROP TABLE event_team_field_configs;
