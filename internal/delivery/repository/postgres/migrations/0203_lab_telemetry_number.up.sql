-- A counter read from a lab telemetry payload (bytes, millicores, restarts, unix
-- time). The payload comes from outside the platform: one negative, fractional,
-- non-numeric or huge value must not stop the rollups (a failed cast or a CHECK
-- keeps the cursor from moving past the observation for good). Anything that
-- is not 1 to 15 plain digits reads as NULL; 15 digits keep the sums of a
-- batch clear of the bigint range.
CREATE FUNCTION lab_telemetry_number(value text) RETURNS bigint
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
AS
$$
SELECT CASE WHEN value ~ '^[0-9]{1,15}$' THEN value::bigint END
$$;
