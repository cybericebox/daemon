-- Moderation list filters by form answers (participants and teams tables).
-- A filter is {"key", "op", "value"?, "values"?}:
--   contains  text answer contains value (case-insensitive)
--   any       single or multi choice answer holds one of values
--   bool      checkbox answer equals value (a missing answer counts as false)
--   present   the answer is (value = true) or is not (value = false) given
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

-- Latest registration-form answers of one participant (NULL without any).
CREATE OR REPLACE FUNCTION event_registration_answers(p_event_id uuid, p_user_id uuid)
    RETURNS jsonb
    LANGUAGE sql
    STABLE
AS
$$
SELECT answer.answers
FROM event_form_answers answer
         JOIN event_form_versions version ON version.id = answer.form_version_id
         JOIN event_forms form ON form.id = version.form_id
WHERE answer.event_id = p_event_id
  AND answer.user_id = p_user_id
  AND form.purpose = 'registration'
ORDER BY answer.submitted_at DESC
LIMIT 1
$$;

-- True when the answers satisfy every filter of the array (empty = true).
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
