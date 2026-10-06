ALTER TABLE event_configs
    ADD COLUMN show_hints boolean NOT NULL DEFAULT true;

UPDATE event_configs
SET show_hints = NOT hints_disabled;

ALTER TABLE event_configs
    DROP COLUMN hints_disabled;
