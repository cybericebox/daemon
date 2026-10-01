DROP INDEX IF EXISTS notification_dispatches_broadcast_recipient_idx;
ALTER TABLE notification_dispatches DROP COLUMN IF EXISTS broadcast_id;
DROP TABLE IF EXISTS notification_broadcasts;
