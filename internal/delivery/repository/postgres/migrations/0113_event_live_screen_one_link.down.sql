ALTER TABLE event_live_screen_links
    DROP CONSTRAINT event_live_screen_links_one_active,
    DROP CONSTRAINT event_live_screen_links_expiry;
UPDATE event_live_screen_links SET expires_at = created_at + interval '60 days' WHERE expires_at IS NULL;
ALTER TABLE event_live_screen_links
    ALTER COLUMN expires_at SET NOT NULL,
    ADD CONSTRAINT event_live_screen_links_check CHECK (expires_at > created_at);
