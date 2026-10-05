DELETE FROM lab_group_cleanup_requests WHERE event_id NOT IN (SELECT id FROM events);
ALTER TABLE lab_group_cleanup_requests
    ADD CONSTRAINT lab_group_cleanup_requests_event_id_fkey FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE;
