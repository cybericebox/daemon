-- Participant countdown on «Завдання» and «Результати»: before the start and
-- during the last N minutes before the effective finish.
ALTER TABLE event_configs
    ADD COLUMN show_start_countdown      boolean NOT NULL DEFAULT true,
    ADD COLUMN show_finish_countdown     boolean NOT NULL DEFAULT true,
    ADD COLUMN finish_countdown_minutes  integer NOT NULL DEFAULT 10
        CHECK (finish_countdown_minutes BETWEEN 1 AND 1440);
