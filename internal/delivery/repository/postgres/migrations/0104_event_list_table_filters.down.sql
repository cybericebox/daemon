DROP FUNCTION IF EXISTS event_list_sort_value(jsonb, text);

-- Restore event_answer_matches without the "range" operator (as in 0098).
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
           ELSE false
           END
$$;
