-- "Selected events" with an empty selection admits no event either.
UPDATE exercises
SET access_level = 1
WHERE access_level = 3;

ALTER TABLE exercises
    DROP CONSTRAINT exercises_access_level_check,
    ADD CONSTRAINT exercises_access_level_check CHECK (access_level IN (0, 1, 2));
