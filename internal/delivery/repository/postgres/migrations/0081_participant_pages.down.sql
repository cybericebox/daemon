ALTER TABLE team_challenges
    DROP COLUMN IF EXISTS content_updated_at;

ALTER TABLE event_configs
    DROP COLUMN IF EXISTS show_hints,
    DROP COLUMN IF EXISTS show_difficulty;
