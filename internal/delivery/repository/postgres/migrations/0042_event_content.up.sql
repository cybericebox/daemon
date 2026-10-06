ALTER TABLE events
    ADD COLUMN landing_document jsonb NOT NULL DEFAULT '{"blocks":[]}'::jsonb,
    ADD COLUMN live_layout_profile text NOT NULL DEFAULT 'scoreboard',
    ADD COLUMN live_layout_slots jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT events_live_layout_profile_check CHECK (live_layout_profile IN ('scoreboard', 'scoreboard_chart', 'presentation'));

CREATE TABLE event_pages (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    slug text NOT NULL,
    title text NOT NULL,
    document jsonb NOT NULL,
    visibility smallint NOT NULL DEFAULT 0 CHECK (visibility IN (0, 1, 2)),
    navigation smallint NOT NULL DEFAULT 0 CHECK (navigation IN (0, 1, 2, 3)),
    navigation_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (event_id, slug)
);

CREATE INDEX event_pages_navigation_idx ON event_pages (event_id, navigation, navigation_order, id);
