-- The "regenerate flags on publish" toggle never had behavior: flags are
-- resolved per team at deploy time. No backward compatibility is kept.
ALTER TABLE exercise_versions DROP COLUMN regen_flags;
