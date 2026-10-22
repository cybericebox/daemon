-- Back to one layout per event: the first screen's layout and draft return to
-- the event; the other screens and their links are dropped.
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS live_layout jsonb NOT NULL DEFAULT '{"version":1,"theme":"dark","aspect":"16:9","screen":{"width":1920,"height":1080,"anchor":"full","textScale":1},"grid":{"cols":12,"rows":8},"widgets":[{"id":"title","type":"title","x":1,"y":1,"w":12,"h":1,"props":{}},{"id":"chart","type":"chart","x":1,"y":2,"w":8,"h":6,"props":{}},{"id":"table","type":"table","x":9,"y":2,"w":4,"h":6,"props":{}},{"id":"organizers","type":"logos","x":1,"y":8,"w":3,"h":1,"props":{"mode":"fixed","title":"Організатори"}},{"id":"partners","type":"logos","x":4,"y":8,"w":9,"h":1,"props":{"mode":"carousel","title":"Партнери"}}]}'::jsonb,
    ADD COLUMN IF NOT EXISTS live_layout_draft jsonb;

UPDATE events AS e
SET live_layout       = first.layout,
    live_layout_draft = first.layout_draft
FROM (SELECT DISTINCT ON (s.event_id) s.event_id, s.layout, s.layout_draft
      FROM event_live_screens AS s
      ORDER BY s.event_id, s.position, s.id) AS first
WHERE first.event_id = e.id;

DELETE
FROM event_live_screen_links AS link
WHERE link.screen_id <> (SELECT s.id
                         FROM event_live_screens AS s
                         WHERE s.event_id = link.event_id
                         ORDER BY s.position, s.id
                         LIMIT 1);

DROP INDEX IF EXISTS event_live_screen_links_screen_idx;
ALTER TABLE event_live_screen_links
    DROP CONSTRAINT IF EXISTS event_live_screen_links_one_active,
    DROP CONSTRAINT IF EXISTS event_live_screen_links_screen_fkey,
    DROP COLUMN IF EXISTS screen_id;
ALTER TABLE event_live_screen_links
    ADD CONSTRAINT event_live_screen_links_one_active EXCLUDE USING btree (event_id WITH =) WHERE (revoked_at IS NULL)
        DEFERRABLE INITIALLY DEFERRED;

DROP TRIGGER IF EXISTS events_live_screen_first ON events;
DROP FUNCTION IF EXISTS event_live_screens_first();
DROP TABLE IF EXISTS event_live_screens;
