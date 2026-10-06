-- The platform now appends its own footer to every email, so the seeded
-- «Cyber ICE Box · Автоматичне повідомлення платформи.» closing line (seeded
-- by 0062 after a divider, renamed by 0094, and copied into the event
-- reminder/finished templates by 0083) is a duplicate. Remove the line, and
-- the divider right before it, only where the line is still exactly the
-- seeded rich-text block; customized templates are left alone.
WITH old_footer(node) AS (
    SELECT jsonb_build_object('type', 'rich_text', 'content', jsonb_build_object(
        'root', jsonb_build_object('type', 'root', 'children', jsonb_build_array(
            jsonb_build_object('type', 'paragraph', 'children', jsonb_build_array(
                jsonb_build_object('type', 'text', 'text', line, 'format', 0)))))))
    FROM unnest(ARRAY['Cyber ICE Box · Автоматичне повідомлення платформи.',
                      'CyberICEBox · Автоматичне повідомлення платформи.']) AS line
),
targets AS (
    SELECT t.id, array_agg(b.ord) AS footer_ords
    FROM notification_email_templates t,
         jsonb_array_elements(t.body) WITH ORDINALITY AS b(elem, ord)
    WHERE jsonb_typeof(t.body) = 'array'
      AND b.elem IN (SELECT node FROM old_footer)
    GROUP BY t.id
)
UPDATE notification_email_templates t
SET body = COALESCE((SELECT jsonb_agg(b.elem ORDER BY b.ord)
                     FROM jsonb_array_elements(t.body) WITH ORDINALITY AS b(elem, ord)
                     WHERE NOT (b.ord = ANY (targets.footer_ords))
                       AND NOT (b.elem = '{"type": "divider"}'::jsonb AND (b.ord + 1) = ANY (targets.footer_ords))),
                    '[]'::jsonb)
FROM targets
WHERE t.id = targets.id;
