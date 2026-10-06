-- «Дата / час» questions answered with a time of day ("HH:MM", no zone):
-- the "time" range type compares it as text, which orders two-digit times.
-- Date bounds may now also be dates ("YYYY-MM-DD") for date-only questions.
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
                   WHEN 'time' THEN
                       COALESCE(p_answers ->> (p_filter ->> 'key'), '') ~ '^\d{2}:\d{2}$'
                           AND (p_filter ->> 'from' IS NULL OR (p_answers ->> (p_filter ->> 'key')) COLLATE "C" >= (p_filter ->> 'from'))
                           AND (p_filter ->> 'to' IS NULL OR (p_answers ->> (p_filter ->> 'key')) COLLATE "C" <= (p_filter ->> 'to'))
                   WHEN 'date' THEN
                       event_try_timestamptz(p_answers ->> (p_filter ->> 'key')) IS NOT NULL
                           AND (p_filter ->> 'from' IS NULL OR event_try_timestamptz(p_answers ->> (p_filter ->> 'key')) >= (p_filter ->> 'from')::timestamptz)
                           AND (p_filter ->> 'to' IS NULL OR event_try_timestamptz(p_answers ->> (p_filter ->> 'key')) <= (p_filter ->> 'to')::timestamptz)
                   ELSE false
                   END
           ELSE false
           END
$$;
