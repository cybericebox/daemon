-- Site banners: one authored line shown under the navbar of every app of its
-- scope (platform = all apps, event = that Event's site). Not a per-user row:
-- visibility is decided at read time from the active window and the audience.
CREATE TABLE site_banners
(
    id             uuid PRIMARY KEY,
    scope_event_id uuid        REFERENCES events (id) ON DELETE CASCADE,
    text           text        NOT NULL,
    link_url       text        NOT NULL DEFAULT '',
    link_label     text        NOT NULL DEFAULT '',
    level          text        NOT NULL DEFAULT 'info',
    active_from    timestamptz,
    active_to      timestamptz,
    dismissible    boolean     NOT NULL DEFAULT true,
    audience       text        NOT NULL DEFAULT 'everyone',
    is_active      boolean     NOT NULL DEFAULT true,
    created_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT site_banners_level_check CHECK (level IN ('info', 'warning', 'critical')),
    CONSTRAINT site_banners_audience_check CHECK (audience IN ('everyone', 'signed_in', 'participants')),
    CONSTRAINT site_banners_window_check CHECK (active_to IS NULL OR active_from IS NULL OR active_to > active_from),
    CONSTRAINT site_banners_participants_event_check CHECK (audience <> 'participants' OR scope_event_id IS NOT NULL)
);

CREATE INDEX site_banners_scope_idx ON site_banners (scope_event_id, created_at DESC);
