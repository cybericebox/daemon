-- 0012 converted the legacy raw-HTML email bodies (text) into jsonb by dumping
-- the ENTIRE HTML string into a single Lexical text node. The block renderer
-- html-escapes text-node content, so those emails ship the HTML as visible code,
-- and Go-template placeholders like {{.RegistrationURL}} sit inside a text node
-- where block substitution never runs — they render verbatim.
--
-- Rewrite the five seeded templates (0004/0006/0008) into PROPER Lexical blocks:
-- paragraphs with real link nodes whose href carries the block-style {{Var}}
-- placeholder (substituted by the renderer) and a variable node for {{Name}}.
--
-- Guard: only touch the still-broken published rows (body still contains the raw
-- "{{." Go-template marker). Admin-edited templates never match, so their edits
-- are preserved.

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hello,","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{RegistrationURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to finish creating your account.","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'continue_registration' AND status = 'published' AND body::text LIKE '%{{.%';

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hi ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":",","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{ResetURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to reset your password. If you did not request this, ignore this email.","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'password_reset' AND status = 'published' AND body::text LIKE '%{{.%';

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hi ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":",","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{ConfirmURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to confirm your new email address.","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'email_confirmation' AND status = 'published' AND body::text LIKE '%{{.%';

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"You have been invited. Click ","format":0},{"type":"link","url":"{{InviteURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to set up your account.","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'user_invitation' AND status = 'published' AND body::text LIKE '%{{.%';

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hi ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":",","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Someone tried to register an account with your email. If this was you, sign in instead. If not, you can ignore this message.","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'account_exists' AND status = 'published' AND body::text LIKE '%{{.%';
