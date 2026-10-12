-- Keep every resolved timestamp; dropping source metadata never retimes a stage.
ALTER TABLE event_stages
    DROP COLUMN closes_with_event,
    DROP COLUMN opens_with_event;
