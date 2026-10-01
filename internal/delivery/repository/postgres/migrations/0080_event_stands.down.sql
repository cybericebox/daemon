DELETE FROM notification_in_app_templates WHERE notification_type = 'event.lab.failed';
DELETE FROM notification_email_templates WHERE notification_type = 'event.lab.failed';
DELETE FROM platform_signal_notification_defaults WHERE signal_type = 'event.lab.failed';

DROP TABLE IF EXISTS event_stand_rollouts;
DROP TABLE IF EXISTS event_team_stands;

ALTER TABLE lab_bindings
    DROP COLUMN IF EXISTS failure_reason,
    DROP COLUMN IF EXISTS deployed_at,
    DROP COLUMN IF EXISTS generation;

DROP INDEX IF EXISTS event_teams_moderators_uq;
DELETE FROM event_teams WHERE moderators;
ALTER TABLE event_teams
    DROP COLUMN IF EXISTS moderators;

ALTER TABLE event_configs
    DROP COLUMN IF EXISTS stand_teardown_delay_minutes,
    DROP COLUMN IF EXISTS stand_deploy_lead_minutes,
    ADD COLUMN dynamic_labs_planned boolean NOT NULL DEFAULT false;

UPDATE event_configs config
SET dynamic_labs_planned = event.infrastructure_allowed
FROM events event
WHERE event.id = config.event_id;

ALTER TABLE events
    DROP COLUMN IF EXISTS infrastructure_allowed;
