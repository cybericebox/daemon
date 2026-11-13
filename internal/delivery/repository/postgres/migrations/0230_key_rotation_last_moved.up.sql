-- When a run last moved a value to the current key: kept across later runs that move nothing, so `keys check` can say
-- how long ago the last value left an old key (the age of the database backups that still need it).
ALTER TABLE key_rotation_progress ADD COLUMN last_moved_at timestamptz;
UPDATE key_rotation_progress SET last_moved_at = updated_at WHERE done > 0;
