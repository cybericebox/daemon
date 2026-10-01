CREATE TABLE notification_email_templates
(
    id                uuid PRIMARY KEY,
    notification_type text        NOT NULL,
    status            text        NOT NULL DEFAULT 'draft',
    subject           text        NOT NULL DEFAULT '',
    preheader         text        NOT NULL DEFAULT '',
    body              text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_email_templates_type_status_idx
    ON notification_email_templates (notification_type, status);

CREATE TABLE notification_in_app_templates
(
    id                uuid PRIMARY KEY,
    notification_type text        NOT NULL,
    status            text        NOT NULL DEFAULT 'draft',
    title             text        NOT NULL DEFAULT '',
    body              text        NOT NULL DEFAULT '',
    link              text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_in_app_templates_type_status_idx
    ON notification_in_app_templates (notification_type, status);

CREATE TABLE notification_settings
(
    notification_type text    NOT NULL,
    channel           text    NOT NULL,
    enabled           boolean NOT NULL DEFAULT false,
    user_can_change   boolean NOT NULL DEFAULT false,
    user_default      boolean NOT NULL DEFAULT false,
    PRIMARY KEY (notification_type, channel)
);

CREATE TABLE notification_user_settings
(
    user_id           uuid    NOT NULL,
    notification_type text    NOT NULL,
    channel           text    NOT NULL,
    enabled           boolean NOT NULL,
    PRIMARY KEY (user_id, notification_type, channel)
);

CREATE TABLE notification_dispatches
(
    id                uuid PRIMARY KEY,
    notification_type text        NOT NULL,
    recipient_user_id uuid        NOT NULL,
    status            text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notification_dispatch_targets
(
    dispatch_id uuid        NOT NULL REFERENCES notification_dispatches (id) ON DELETE CASCADE,
    channel     text        NOT NULL,
    status      text        NOT NULL,
    error       text        NOT NULL DEFAULT '',
    attempts    int         NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (dispatch_id, channel)
);

CREATE TABLE in_app_notifications
(
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL,
    title      text        NOT NULL DEFAULT '',
    body       text        NOT NULL DEFAULT '',
    link       text        NOT NULL DEFAULT '',
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX in_app_notifications_user_idx ON in_app_notifications (user_id, created_at DESC);
