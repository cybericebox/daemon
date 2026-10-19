ALTER TABLE exercise_versions
    DROP COLUMN avg_difficulty;

DROP FUNCTION exercise_variants_avg_difficulty(jsonb);
