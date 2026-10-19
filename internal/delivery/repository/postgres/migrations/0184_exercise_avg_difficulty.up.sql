-- The average difficulty of a version: the mean level (elementary 1 … insane 6) of the subtasks of its FIRST variant
-- (every variant has the same subtasks at the same levels). NULL when there is no variant, no subtask, or no subtask
-- with a known level. A generated column, so every write of the variants keeps it current.
CREATE FUNCTION exercise_variants_avg_difficulty(p_variants jsonb)
    RETURNS numeric
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
AS
$$
SELECT round(avg(CASE task ->> 'difficulty'
                     WHEN 'elementary' THEN 1
                     WHEN 'trivial' THEN 2
                     WHEN 'easy' THEN 3
                     WHEN 'medium' THEN 4
                     WHEN 'hard' THEN 5
                     WHEN 'insane' THEN 6
                 END), 2)
FROM jsonb_array_elements(CASE
                              WHEN jsonb_typeof(p_variants) = 'array'
                                  AND jsonb_typeof(p_variants -> 0 -> 'tasks') = 'array'
                                  THEN p_variants -> 0 -> 'tasks'
                              ELSE '[]'::jsonb
                          END) AS task
$$;

ALTER TABLE exercise_versions
    ADD COLUMN avg_difficulty numeric(3, 2) GENERATED ALWAYS AS (exercise_variants_avg_difficulty(variants)) STORED;
