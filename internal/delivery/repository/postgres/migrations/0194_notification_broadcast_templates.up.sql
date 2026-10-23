-- Custom templates: authored broadcast content (channels, the email and the
-- in-app parts) saved for sending by hand, not bound to any platform action.
-- The UI lists them beside the triggered templates as their own kind, but they
-- are stored apart: the triggered tables hold one draft/published version per
-- action type and channel, resolved by the dispatcher by action type, while a
-- custom template is one named record over both channels with no versions. A template
-- belongs to one scope: the platform (scope_event_id NULL, admins with the
-- broadcast permission) or one Event (its owners and write moderators). The
-- audience is chosen when sending, so it is not stored here.
CREATE TABLE notification_broadcast_templates
(
    id             uuid PRIMARY KEY,
    scope_event_id uuid        REFERENCES events (id) ON DELETE CASCADE,
    name           text        NOT NULL CHECK (btrim(name) <> ''),
    channels       text[]      NOT NULL,
    subject        text        NOT NULL DEFAULT '',
    preheader      text        NOT NULL DEFAULT '',
    email_body     jsonb       NOT NULL DEFAULT '[]',
    email_styling  jsonb       NOT NULL DEFAULT '{}',
    inapp_title    text        NOT NULL DEFAULT '',
    inapp_body     text        NOT NULL DEFAULT '',
    inapp_link     text        NOT NULL DEFAULT '',
    created_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
    updated_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- One name per scope (case-insensitive), so the picker never shows two alike.
CREATE UNIQUE INDEX notification_broadcast_templates_name_idx
    ON notification_broadcast_templates (COALESCE(scope_event_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(btrim(name)));
