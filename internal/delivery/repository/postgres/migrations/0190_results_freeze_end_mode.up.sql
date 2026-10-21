ALTER TABLE event_configs
    ADD COLUMN results_freeze_end_mode text NOT NULL DEFAULT 'at_finish'
        CONSTRAINT event_configs_results_freeze_end_mode_check
        CHECK (results_freeze_end_mode IN ('at_finish', 'manual')),
    ADD COLUMN results_freeze_banner_text text NOT NULL DEFAULT ''
        CONSTRAINT event_configs_results_freeze_banner_text_check
        CHECK (char_length(results_freeze_banner_text) <= 280),
    ADD COLUMN results_freeze_banner_level text NOT NULL DEFAULT 'warning'
        CONSTRAINT event_configs_results_freeze_banner_level_check
        CHECK (results_freeze_banner_level IN ('info', 'warning', 'critical'));
