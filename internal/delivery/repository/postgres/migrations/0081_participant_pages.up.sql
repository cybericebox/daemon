-- W3 participant pages: board presentation toggles and the content-replacement
-- marker. Nothing writes content_updated_at yet; W4 content replacement will.
ALTER TABLE event_configs
    ADD COLUMN show_difficulty boolean NOT NULL DEFAULT true,
    ADD COLUMN show_hints      boolean NOT NULL DEFAULT true;

ALTER TABLE team_challenges
    ADD COLUMN content_updated_at timestamptz NULL;
