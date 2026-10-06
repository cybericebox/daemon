DROP TABLE IF EXISTS event_pages;

ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_live_layout_profile_check,
    DROP COLUMN IF EXISTS live_layout_slots,
    DROP COLUMN IF EXISTS live_layout_profile,
    DROP COLUMN IF EXISTS landing_document;
