-- access_level 3 = no event may use the catalog exercise.
-- exercise_available_to_event admits only levels 0-2, so level 3 is denied
-- everywhere it is consulted (attach, event catalog, manager reads, lists);
-- existing attachments keep their pinned versions.
ALTER TABLE exercises
    DROP CONSTRAINT exercises_access_level_check,
    ADD CONSTRAINT exercises_access_level_check CHECK (access_level IN (0, 1, 2, 3));
