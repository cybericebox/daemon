-- One screen link per event (owner decision): at most one unrevoked link,
-- «Без обмеження» expiry (NULL), and a replace that revokes the old link
-- and inserts the new one in one statement (the constraint is checked at
-- commit, so the order inside the statement does not matter).
UPDATE event_live_screen_links AS link
SET revoked_at = now()
WHERE link.revoked_at IS NULL
  AND EXISTS (SELECT 1
              FROM event_live_screen_links AS newer
              WHERE newer.event_id = link.event_id
                AND newer.revoked_at IS NULL
                AND newer.created_at > link.created_at);

ALTER TABLE event_live_screen_links
    ALTER COLUMN expires_at DROP NOT NULL,
    DROP CONSTRAINT event_live_screen_links_check,
    ADD CONSTRAINT event_live_screen_links_expiry CHECK (expires_at IS NULL OR expires_at > created_at),
    ADD CONSTRAINT event_live_screen_links_one_active EXCLUDE USING btree (event_id WITH =) WHERE (revoked_at IS NULL)
        DEFERRABLE INITIALLY DEFERRED;
