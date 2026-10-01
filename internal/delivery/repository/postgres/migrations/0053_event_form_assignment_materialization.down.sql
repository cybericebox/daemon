DROP INDEX event_form_assignments_due_idx;

ALTER TABLE event_form_assignments
    DROP COLUMN materialized_at;
