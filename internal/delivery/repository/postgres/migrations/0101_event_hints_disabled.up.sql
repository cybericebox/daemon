-- Hints can only be hidden event-wide, never forced on: show_hints becomes
-- hints_disabled. A task shows hints when it enables them and the event does
-- not disable them, so the inverted flag keeps today's visibility.
ALTER TABLE event_configs
    ADD COLUMN hints_disabled boolean NOT NULL DEFAULT false;

UPDATE event_configs
SET hints_disabled = NOT show_hints;

ALTER TABLE event_configs
    DROP COLUMN show_hints;
