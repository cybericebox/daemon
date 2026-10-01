DROP INDEX IF EXISTS event_challenges_group_idx;
DROP INDEX IF EXISTS event_challenge_groups_event_order_idx;
ALTER TABLE event_challenges DROP COLUMN IF EXISTS group_id;
DROP TABLE IF EXISTS event_challenge_groups;
