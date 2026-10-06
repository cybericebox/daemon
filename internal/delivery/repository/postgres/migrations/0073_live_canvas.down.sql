ALTER TABLE events
    DROP COLUMN live_layout_draft,
    DROP COLUMN live_layout,
    ADD COLUMN live_layout_profile text NOT NULL DEFAULT 'scoreboard',
    ADD COLUMN live_layout_slots jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT events_live_layout_profile_check CHECK (live_layout_profile IN ('scoreboard', 'scoreboard_chart', 'presentation'));
