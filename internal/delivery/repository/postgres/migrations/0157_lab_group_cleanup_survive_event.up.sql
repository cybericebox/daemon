-- A deleted event must still have its LabGroups torn down, so the durable cleanup requests outlive the
-- event row: drop the cascading foreign key (event_id stays as a plain reference for diagnostics).
ALTER TABLE lab_group_cleanup_requests
    DROP CONSTRAINT lab_group_cleanup_requests_event_id_fkey;
