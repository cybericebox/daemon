-- A declined team belongs to the preparation tuple (task revision, reveal mode) it was declined in. When the tuple
-- changes (the barrier is invalidated) the decline no longer applies: the team joins the new cohort again.
ALTER TABLE event_lab_reveal_declined
    ADD COLUMN revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN mode     text   NOT NULL DEFAULT '';
