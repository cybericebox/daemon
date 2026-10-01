-- Typed operators for manage table filters: number ranges may exclude a
-- bound (=, >, <, between), and date ranges accept any date-like answer
-- ("YYYY-MM-DD" date questions, UTC ISO datetimes, the table's own
-- timestamps). A value that is not a date never matches instead of failing
-- the query.
CREATE OR REPLACE FUNCTION event_try_timestamptz(p_value text)
    RETURNS timestamptz
    LANGUAGE plpgsql
    STABLE
AS
$$
BEGIN
    IF p_value IS NULL OR p_value !~ '^\d{4}-\d{2}-\d{2}' THEN
        RETURN NULL;
    END IF;
    RETURN p_value::timestamptz;
EXCEPTION
    WHEN others THEN RETURN NULL;
END
$$;

CREATE OR REPLACE FUNCTION event_answer_matches(p_answers jsonb, p_filter jsonb)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT CASE p_filter ->> 'op'
           WHEN 'contains' THEN
               strpos(lower(COALESCE(COALESCE(p_answers, '{}'::jsonb) ->> (p_filter ->> 'key'), '')),
                      lower(COALESCE(p_filter ->> 'value', ''))) > 0
           WHEN 'any' THEN
               CASE jsonb_typeof(p_answers -> (p_filter ->> 'key'))
                   WHEN 'array' THEN EXISTS (SELECT 1
                                             FROM jsonb_array_elements_text(p_answers -> (p_filter ->> 'key')) answer
                                             WHERE answer IN (SELECT jsonb_array_elements_text(p_filter -> 'values')))
                   WHEN 'string' THEN (p_answers ->> (p_filter ->> 'key')) IN (SELECT jsonb_array_elements_text(p_filter -> 'values'))
                   ELSE false
                   END
           WHEN 'bool' THEN
               (COALESCE(p_answers -> (p_filter ->> 'key'), 'false'::jsonb) = 'true'::jsonb) = (p_filter -> 'value' = 'true'::jsonb)
           WHEN 'present' THEN
               (COALESCE(p_answers -> (p_filter ->> 'key'), 'null'::jsonb) NOT IN ('null'::jsonb, '""'::jsonb, '[]'::jsonb, '{}'::jsonb))
                   = (p_filter -> 'value' = 'true'::jsonb)
           WHEN 'range' THEN
               CASE p_filter ->> 'type'
                   WHEN 'number' THEN
                       jsonb_typeof(p_answers -> (p_filter ->> 'key')) = 'number'
                           AND (p_filter ->> 'from' IS NULL OR CASE WHEN p_filter -> 'fromExclusive' = 'true'::jsonb
                                   THEN (p_answers ->> (p_filter ->> 'key'))::numeric > (p_filter ->> 'from')::numeric
                                   ELSE (p_answers ->> (p_filter ->> 'key'))::numeric >= (p_filter ->> 'from')::numeric END)
                           AND (p_filter ->> 'to' IS NULL OR CASE WHEN p_filter -> 'toExclusive' = 'true'::jsonb
                                   THEN (p_answers ->> (p_filter ->> 'key'))::numeric < (p_filter ->> 'to')::numeric
                                   ELSE (p_answers ->> (p_filter ->> 'key'))::numeric <= (p_filter ->> 'to')::numeric END)
                   WHEN 'date' THEN
                       event_try_timestamptz(p_answers ->> (p_filter ->> 'key')) IS NOT NULL
                           AND (p_filter ->> 'from' IS NULL OR event_try_timestamptz(p_answers ->> (p_filter ->> 'key')) >= (p_filter ->> 'from')::timestamptz)
                           AND (p_filter ->> 'to' IS NULL OR event_try_timestamptz(p_answers ->> (p_filter ->> 'key')) <= (p_filter ->> 'to')::timestamptz)
                   ELSE false
                   END
           ELSE false
           END
$$;

-- The matchers read the session time zone for date-only answers.
CREATE OR REPLACE FUNCTION event_answers_match_all(p_answers jsonb, p_filters jsonb)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT NOT EXISTS (SELECT 1
                   FROM jsonb_array_elements(COALESCE(p_filters, '[]'::jsonb)) filter
                   WHERE NOT event_answer_matches(p_answers, filter))
$$;
