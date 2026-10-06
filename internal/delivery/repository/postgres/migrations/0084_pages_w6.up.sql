-- W6 page constructor: drafts for the landing and content pages, navbar-only
-- navigation, no block-level alignment on text blocks.

-- The existing columns stay the published version; drafts are separate.
ALTER TABLE events ADD COLUMN landing_draft jsonb;

ALTER TABLE event_pages
    ADD COLUMN draft        jsonb,
    ADD COLUMN published_at timestamptz;

-- Everything that exists today is live on the site.
UPDATE event_pages SET published_at = updated_at;

-- The event site has one menu (navbar): sidebar/both placements become navbar.
UPDATE event_pages SET navigation = 1 WHERE navigation IN (2, 3);
ALTER TABLE event_pages DROP CONSTRAINT event_pages_navigation_check;
ALTER TABLE event_pages ADD CONSTRAINT event_pages_navigation_check CHECK (navigation IN (0, 1));

-- Rich text aligns each paragraph itself; the block-level key was never rendered.
UPDATE events
SET landing_document = jsonb_set(landing_document, '{blocks}', (
    SELECT jsonb_agg(CASE WHEN block ->> 'type' = 'text' THEN block - 'layout' ELSE block END ORDER BY position)
    FROM jsonb_array_elements(landing_document -> 'blocks') WITH ORDINALITY AS item(block, position)
))
WHERE jsonb_path_exists(landing_document, '$.blocks[*] ? (@.type == "text" && exists(@.layout))');

UPDATE event_pages
SET document = jsonb_set(document, '{blocks}', (
    SELECT jsonb_agg(CASE WHEN block ->> 'type' = 'text' THEN block - 'layout' ELSE block END ORDER BY position)
    FROM jsonb_array_elements(document -> 'blocks') WITH ORDINALITY AS item(block, position)
))
WHERE jsonb_path_exists(document, '$.blocks[*] ? (@.type == "text" && exists(@.layout))');
