-- Who besides the event staff may open the live screen: 0 staff, 1 approved
-- participants, 2 everyone. The results visibility still caps it.
ALTER TABLE event_configs
    ADD COLUMN results_live_audience smallint NOT NULL DEFAULT 0
        CHECK (results_live_audience BETWEEN 0 AND 2);
