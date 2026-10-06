-- A timed assignment snapshots its current recipients exactly once. Later
-- participants are handled only when include_future_participants is enabled.
ALTER TABLE event_form_assignments
    ADD COLUMN materialized_at timestamptz;

CREATE INDEX event_form_assignments_due_idx
    ON event_form_assignments (at)
    WHERE enabled AND trigger = 'at_time' AND materialized_at IS NULL;
