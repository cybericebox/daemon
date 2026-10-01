-- Restore the matchers of 0104 (inclusive ranges, dates from the table only).
CREATE OR REPLACE FUNCTION event_answer_matches(p_answers jsonb, p_filter jsonb)
    RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
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
                           AND (p_filter ->> 'from' IS NULL OR (p_answers ->> (p_filter ->> 'key'))::numeric >= (p_filter ->> 'from')::numeric)
                           AND (p_filter ->> 'to' IS NULL OR (p_answers ->> (p_filter ->> 'key'))::numeric <= (p_filter ->> 'to')::numeric)
                   WHEN 'date' THEN
                       jsonb_typeof(p_answers -> (p_filter ->> 'key')) = 'string'
                           AND (p_filter ->> 'from' IS NULL OR (p_answers ->> (p_filter ->> 'key'))::timestamptz >= (p_filter ->> 'from')::timestamptz)
                           AND (p_filter ->> 'to' IS NULL OR (p_answers ->> (p_filter ->> 'key'))::timestamptz <= (p_filter ->> 'to')::timestamptz)
                   ELSE false
                   END
           ELSE false
           END
$$;

CREATE OR REPLACE FUNCTION event_answers_match_all(p_answers jsonb, p_filters jsonb)
    RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT NOT EXISTS (SELECT 1
                   FROM jsonb_array_elements(COALESCE(p_filters, '[]'::jsonb)) filter
                   WHERE NOT event_answer_matches(p_answers, filter))
$$;

DROP FUNCTION IF EXISTS event_try_timestamptz(text);
