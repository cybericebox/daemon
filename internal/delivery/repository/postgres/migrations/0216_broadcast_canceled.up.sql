-- A running broadcast can be canceled: the sender stops queuing recipients.
ALTER TABLE notification_broadcasts
    DROP CONSTRAINT notification_broadcasts_status_check,
    ADD CONSTRAINT notification_broadcasts_status_check CHECK (status IN ('sending', 'done', 'failed', 'canceled'));
