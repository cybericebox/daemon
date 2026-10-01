ALTER TABLE event_configs
    ADD COLUMN results_live_audience smallint NOT NULL DEFAULT 0
        CHECK (results_live_audience BETWEEN 0 AND 2);
