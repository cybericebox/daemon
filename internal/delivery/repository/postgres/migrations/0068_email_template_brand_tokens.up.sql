-- Reseed platform (scope_event_id IS NULL) email templates so they follow the
-- shared brand tokens instead of the hardcoded hexes 0062 seeded, and lead
-- with a logo block. Idempotent: only rows still carrying an old hex are
-- rewritten, and the logo block is prepended only when one is not already
-- present anywhere in body.

UPDATE notification_email_templates t
SET styling = t.styling
    || jsonb_strip_nulls(jsonb_build_object(
           'heading_color',
           CASE WHEN lower(t.styling ->> 'heading_color') = ANY (ARRAY ['#221b54', '#292841', '#211a52'])
                    THEN 'theme:brand' END,
           'cta_bg_color',
           CASE WHEN lower(t.styling ->> 'cta_bg_color') = ANY (ARRAY ['#221b54', '#292841', '#211a52'])
                    THEN 'theme:accent' END,
           'cta_text_color',
           CASE WHEN lower(t.styling ->> 'cta_text_color') = '#ffffff'
                    THEN 'theme:on_accent' END
       ))
WHERE t.scope_event_id IS NULL
  AND t.status IN ('draft', 'published');

UPDATE notification_email_templates t
SET body = jsonb_build_array(jsonb_build_object('type', 'logo', 'align', 'center', 'width_px', 64)) || t.body
WHERE t.scope_event_id IS NULL
  AND t.status IN ('draft', 'published')
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_array_elements(t.body) elem WHERE elem ->> 'type' = 'logo'
  );
