-- Not reversible on purpose: the erasure queue holds the work of removing people from the telemetry that is not
-- done yet, and dropping it would leave their names in place. Nothing is done; drop the objects by hand once the
-- queue is empty.
SELECT 1;
