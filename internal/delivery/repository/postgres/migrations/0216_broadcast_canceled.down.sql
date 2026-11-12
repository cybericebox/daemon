UPDATE notification_broadcasts SET status = 'done' WHERE status = 'canceled';
ALTER TABLE notification_broadcasts
    DROP CONSTRAINT notification_broadcasts_status_check,
    ADD CONSTRAINT notification_broadcasts_status_check CHECK (status IN ('sending', 'done', 'failed'));
