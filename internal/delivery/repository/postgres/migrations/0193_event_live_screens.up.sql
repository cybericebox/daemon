-- Several live screens per event (up to 10, a limit the application keeps):
-- each has its own name, published layout, draft and screen link. The single
-- layout and draft of the event become its first screen «Екран 1», unchanged,
-- and the event's screen link belongs to that screen. Re-running is a no-op.
CREATE TABLE IF NOT EXISTS event_live_screens
(
    id           uuid PRIMARY KEY,
    event_id     uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    position     integer     NOT NULL,
    name         text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40 AND name = btrim(name)),
    layout       jsonb       NOT NULL DEFAULT '{"version":1,"theme":"dark","aspect":"16:9","screen":{"width":1920,"height":1080,"anchor":"full","textScale":1},"grid":{"cols":12,"rows":8},"widgets":[{"id":"title","type":"title","x":1,"y":1,"w":12,"h":1,"props":{}},{"id":"chart","type":"chart","x":1,"y":2,"w":8,"h":6,"props":{}},{"id":"table","type":"table","x":9,"y":2,"w":4,"h":6,"props":{}},{"id":"organizers","type":"logos","x":1,"y":8,"w":3,"h":1,"props":{"mode":"fixed","title":"Організатори"}},{"id":"partners","type":"logos","x":4,"y":8,"w":9,"h":1,"props":{"mode":"carousel","title":"Партнери"}}]}'::jsonb,
    layout_draft jsonb       NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- A link names its screen together with its event (see below).
    UNIQUE (id, event_id)
);

CREATE INDEX IF NOT EXISTS event_live_screens_event_idx ON event_live_screens (event_id, position, id);

-- The event's layout and draft move over as they are (version included).
DO
$$
    BEGIN
        IF EXISTS (SELECT 1
                   FROM information_schema.columns
                   WHERE table_schema = current_schema()
                     AND table_name = 'events'
                     AND column_name = 'live_layout') THEN
            INSERT INTO event_live_screens (id, event_id, position, name, layout, layout_draft)
            SELECT gen_random_uuid(), e.id, 0, 'Екран 1', e.live_layout, e.live_layout_draft
            FROM events AS e
            WHERE NOT EXISTS (SELECT 1 FROM event_live_screens AS s WHERE s.event_id = e.id);
        END IF;
    END
$$;

-- Events without a screen (none after the copy above) and every new event get
-- «Екран 1» with the default layout.
INSERT INTO event_live_screens (id, event_id, position, name)
SELECT gen_random_uuid(), e.id, 0, 'Екран 1'
FROM events AS e
WHERE NOT EXISTS (SELECT 1 FROM event_live_screens AS s WHERE s.event_id = e.id);

CREATE OR REPLACE FUNCTION event_live_screens_first() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    INSERT INTO event_live_screens (id, event_id, position, name) VALUES (gen_random_uuid(), NEW.id, 0, 'Екран 1');
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS events_live_screen_first ON events;
CREATE TRIGGER events_live_screen_first
    AFTER INSERT
    ON events
    FOR EACH ROW
EXECUTE FUNCTION event_live_screens_first();

ALTER TABLE events
    DROP COLUMN IF EXISTS live_layout_draft,
    DROP COLUMN IF EXISTS live_layout;

-- A link belongs to one screen: the existing links (working or not) to the
-- event's first screen; one working link per screen. Deleting a screen
-- deletes its links, so its URL stops working.
ALTER TABLE event_live_screen_links
    ADD COLUMN IF NOT EXISTS screen_id uuid;

UPDATE event_live_screen_links AS link
SET screen_id = (SELECT s.id
                 FROM event_live_screens AS s
                 WHERE s.event_id = link.event_id
                 ORDER BY s.position, s.id
                 LIMIT 1)
WHERE link.screen_id IS NULL;

ALTER TABLE event_live_screen_links
    ALTER COLUMN screen_id SET NOT NULL,
    DROP CONSTRAINT IF EXISTS event_live_screen_links_screen_fkey,
    DROP CONSTRAINT IF EXISTS event_live_screen_links_one_active;

ALTER TABLE event_live_screen_links
    ADD CONSTRAINT event_live_screen_links_screen_fkey FOREIGN KEY (screen_id, event_id)
        REFERENCES event_live_screens (id, event_id) ON DELETE CASCADE,
    ADD CONSTRAINT event_live_screen_links_one_active EXCLUDE USING btree (screen_id WITH =) WHERE (revoked_at IS NULL)
        DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX IF NOT EXISTS event_live_screen_links_screen_idx ON event_live_screen_links (screen_id);
