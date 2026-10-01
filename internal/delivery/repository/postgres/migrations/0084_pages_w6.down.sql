-- Drafts are discarded; navbar-only placements and stripped text layout stay.
ALTER TABLE event_pages DROP CONSTRAINT event_pages_navigation_check;
ALTER TABLE event_pages ADD CONSTRAINT event_pages_navigation_check CHECK (navigation IN (0, 1, 2, 3));

DELETE FROM event_pages WHERE published_at IS NULL;

ALTER TABLE event_pages
    DROP COLUMN draft,
    DROP COLUMN published_at;

ALTER TABLE events DROP COLUMN landing_draft;
