-- W5 team stands: the admin-owned infrastructure flag, stand timing, the
-- hidden moderators team, lab generations and the persisted stand status.

-- The infrastructure flag moves from the moderator config to the admin-owned
-- event row. UpdateEvent never writes it, so it is immutable after creation.
ALTER TABLE events
    ADD COLUMN infrastructure_allowed boolean NOT NULL DEFAULT false;

UPDATE events event
SET infrastructure_allowed = config.dynamic_labs_planned
FROM event_configs config
WHERE config.event_id = event.id;

ALTER TABLE event_configs
    DROP COLUMN dynamic_labs_planned,
    ADD COLUMN stand_deploy_lead_minutes integer NOT NULL DEFAULT 30
        CHECK (stand_deploy_lead_minutes BETWEEN 5 AND 1440),
    ADD COLUMN stand_teardown_delay_minutes integer NOT NULL DEFAULT 60
        CHECK (stand_teardown_delay_minutes BETWEEN 0 AND 10080);

ALTER TABLE event_teams
    ADD COLUMN moderators boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX event_teams_moderators_uq
    ON event_teams (event_id)
    WHERE moderators;

ALTER TABLE lab_bindings
    ADD COLUMN generation integer NOT NULL DEFAULT 0,
    ADD COLUMN deployed_at timestamptz,
    ADD COLUMN failure_reason text;

-- One row per team stand. No row means the stand was never deployed.
-- status: 1 creating, 2 ready, 3 failed, 4 removed.
CREATE TABLE event_team_stands
(
    event_team_id     uuid        PRIMARY KEY REFERENCES event_teams (id) ON DELETE CASCADE,
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    status            smallint    NOT NULL CHECK (status IN (1, 2, 3, 4)),
    reason            text,
    generation        integer     NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    status_changed_at timestamptz NOT NULL
);

CREATE INDEX event_team_stands_event_idx ON event_team_stands (event_id);

-- Event-level rollout state: opened_at is the sticky strict-availability
-- barrier for infrastructure challenges; torn_down_at ends stand work.
CREATE TABLE event_stand_rollouts
(
    event_id     uuid        PRIMARY KEY REFERENCES events (id) ON DELETE CASCADE,
    opened_at    timestamptz,
    torn_down_at timestamptz,
    updated_at   timestamptz NOT NULL
);

-- event.lab.failed is moderator-facing and platform-owned: in-app on,
-- email off by default. Templates copy the manager assignment styling.
INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
VALUES ('event.lab.failed', 'in_app', true, '{"kind":"signal_subject"}'::jsonb),
       ('event.lab.failed', 'email', false, '{"kind":"signal_subject"}'::jsonb)
ON CONFLICT (signal_type, channel) DO NOTHING;

INSERT INTO notification_email_templates
    (id, notification_type, status, subject, preheader, body, styling, published_at)
SELECT gen_random_uuid(), 'event.lab.failed', 'published',
       'Помилка стенда команди «{{.team_name}}»', 'Стенд потребує вашої уваги',
       jsonb_set(body, ARRAY[(SELECT (block_index - 1)::text
                              FROM jsonb_array_elements(body) WITH ORDINALITY AS block(value, block_index)
                              WHERE value->>'type' = 'rich_text' LIMIT 1)],
           '{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Стенд команди «","format":0},{"type":"variable","varName":"team_name"},{"type":"text","text":"» на заході «","format":0},{"type":"variable","varName":"event_name"},{"type":"text","text":"» не вдалося розгорнути. Причина: ","format":0},{"type":"variable","varName":"reason"},{"type":"text","text":".","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Завдання зі стендом не відкриються, доки стенди не будуть готові в усіх команд. Перевірте розділ «Стенди» і перестворіть стенд.","format":0}]}]}}}'::jsonb),
       styling, now()
FROM notification_email_templates
WHERE notification_type = 'event.manager.assigned'
  AND scope_event_id IS NULL
  AND status = 'published';

INSERT INTO notification_in_app_templates
    (id, notification_type, status, title, body, link, icon, tone, surface,
     auto_dismiss_ms, actions, dismissible, published_at)
SELECT gen_random_uuid(), 'event.lab.failed', 'published',
       'Помилка стенда', 'Стенд команди «{{.team_name}}» на заході «{{.event_name}}» не розгорнуто: {{.reason}}.',
       '', 'warning', 'warning', surface, auto_dismiss_ms, actions, dismissible, now()
FROM notification_in_app_templates
WHERE notification_type = 'event.manager.assigned'
  AND scope_event_id IS NULL
  AND status = 'published';
