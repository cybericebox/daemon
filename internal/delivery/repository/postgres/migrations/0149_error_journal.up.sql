-- Platform error journal: errors grouped by fingerprint, a few recent samples per group,
-- daily 404 counters, and the notification settings. No IP addresses anywhere.

CREATE TABLE error_groups
(
    id                 uuid PRIMARY KEY,
    fingerprint        text        NOT NULL UNIQUE,
    kind               text        NOT NULL,
    source             text        NOT NULL DEFAULT '',
    title              text        NOT NULL,
    status             text        NOT NULL DEFAULT 'open',
    occurrences        bigint      NOT NULL DEFAULT 0,
    first_seen_at      timestamptz NOT NULL,
    last_seen_at       timestamptz NOT NULL,
    resolved_at        timestamptz,
    last_notified_at   timestamptz,
    suppressed_since   bigint      NOT NULL DEFAULT 0,
    CONSTRAINT error_groups_status_check CHECK (status IN ('open', 'resolved', 'ignored'))
);
CREATE INDEX error_groups_last_seen_idx ON error_groups (last_seen_at DESC);
CREATE INDEX error_groups_kind_status_idx ON error_groups (kind, status, last_seen_at DESC);

COMMENT ON TABLE error_groups IS 'Platform error journal: one row per fingerprint (kind + route template / job kind + normalized message)';
COMMENT ON COLUMN error_groups.kind IS 'http_5xx, panic, http_403, http_429, job, mail, lab_agent_offline, lab_deploy, lab_cert_expiry, lab_component';
COMMENT ON COLUMN error_groups.source IS 'Route template, job kind or agent name; never a raw path';
COMMENT ON COLUMN error_groups.suppressed_since IS 'Occurrences since the last notification (rate limiting: a storm is one message with a count)';

CREATE TABLE error_samples
(
    id          uuid PRIMARY KEY,
    group_id    uuid        NOT NULL REFERENCES error_groups (id) ON DELETE CASCADE,
    occurred_at timestamptz NOT NULL,
    message     text        NOT NULL,
    stack       text        NOT NULL DEFAULT '',
    method      text        NOT NULL DEFAULT '',
    route       text        NOT NULL DEFAULT '',
    http_status integer,
    request_id  text        NOT NULL DEFAULT '',
    user_id     uuid,
    role        text        NOT NULL DEFAULT '',
    permission  text        NOT NULL DEFAULT '',
    limiter     text        NOT NULL DEFAULT '',
    details     jsonb       NOT NULL DEFAULT '{}'
);
CREATE INDEX error_samples_group_idx ON error_samples (group_id, occurred_at DESC);
CREATE INDEX error_samples_occurred_idx ON error_samples (occurred_at);

COMMENT ON TABLE error_samples IS 'A few recent samples per error group (trimmed on insert); messages are scrubbed of secrets, tokens and e-mail addresses; user_id has no foreign key (a deleted user must not break the journal)';

CREATE TABLE error_not_found_daily
(
    day   date    NOT NULL,
    route text    NOT NULL,
    hits  bigint  NOT NULL DEFAULT 0,
    PRIMARY KEY (day, route)
);

COMMENT ON TABLE error_not_found_daily IS 'Daily 404 counters: route template for handler 404s, empty route = unmatched paths (paths are never stored)';

CREATE TABLE error_journal_settings
(
    id                          boolean PRIMARY KEY DEFAULT true CHECK (id),
    notify_emails               text[]      NOT NULL DEFAULT '{}',
    email_to_super_admins       boolean     NOT NULL DEFAULT true,
    updated_at                  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO error_journal_settings (id) VALUES (true);

COMMENT ON TABLE error_journal_settings IS 'Singleton: e-mail recipients of error notifications (empty list + email_to_super_admins = all super admins)';

CREATE TABLE error_journal_telegram_chats
(
    chat_id       text PRIMARY KEY,
    label         text        NOT NULL DEFAULT '',
    failing       boolean     NOT NULL DEFAULT false,
    failing_since timestamptz,
    last_error    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL
);

COMMENT ON TABLE error_journal_telegram_chats IS 'Telegram chat ids (persons or groups) that receive error notifications; failing = the bot got 403 (blocked / removed), kept visible instead of dropped';
