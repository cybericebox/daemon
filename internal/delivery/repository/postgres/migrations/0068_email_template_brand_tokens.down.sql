-- Reverse 0068: drop the leading logo block and restore the literal hexes
-- the brand tokens replaced.

UPDATE notification_email_templates t
SET body = t.body - 0
WHERE t.scope_event_id IS NULL
  AND t.status IN ('draft', 'published')
  AND t.body -> 0 ->> 'type' = 'logo';

UPDATE notification_email_templates t
SET styling = t.styling
    || jsonb_strip_nulls(jsonb_build_object(
           'heading_color', CASE WHEN t.styling ->> 'heading_color' = 'theme:brand' THEN '#221b54' END,
           'cta_bg_color', CASE WHEN t.styling ->> 'cta_bg_color' = 'theme:accent' THEN '#221b54' END,
           'cta_text_color', CASE WHEN t.styling ->> 'cta_text_color' = 'theme:on_accent' THEN '#FFFFFF' END
       ))
WHERE t.scope_event_id IS NULL
  AND t.status IN ('draft', 'published');
