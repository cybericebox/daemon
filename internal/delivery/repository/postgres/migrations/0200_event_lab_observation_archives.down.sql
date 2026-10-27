-- Not reversible on purpose: rolling this back would drop the archive, and by then the archive may hold the only copy
-- of raw telemetry (the raw rows are deleted a day after they were archived). Nothing is done; drop the tables by hand
-- if the data is really to go.
SELECT 1;
