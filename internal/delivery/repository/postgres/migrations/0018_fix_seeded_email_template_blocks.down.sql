-- Revert each template to the pre-0018 (0012-style) shape: the raw HTML string
-- wrapped in a single Lexical text node. Guarded by exact jsonb equality with the
-- body the up migration set, so a template edited in the admin after up is left
-- untouched (only our own rewrite is rolled back).

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"<p>Hello,</p><p>Click <a href=\"{{.RegistrationURL}}\">here</a> to finish creating your account.</p>","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'continue_registration' AND status = 'published'
  AND body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hello,","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{RegistrationURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to finish creating your account.","format":0}]}]}}}]'::jsonb;

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"<p>Hi {{.Name}},</p><p>Click <a href=\"{{.ResetURL}}\">here</a> to reset your password. If you did not request this, ignore this email.</p>","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'password_reset' AND status = 'published'
  AND body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hi ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":",","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{ResetURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to reset your password. If you did not request this, ignore this email.","format":0}]}]}}}]'::jsonb;

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"<p>Hi {{.Name}},</p><p>Click <a href=\"{{.ConfirmURL}}\">here</a> to confirm your new email address.</p>","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'email_confirmation' AND status = 'published'
  AND body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hi ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":",","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{ConfirmURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to confirm your new email address.","format":0}]}]}}}]'::jsonb;

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"<p>You have been invited. Click <a href=\"{{.InviteURL}}\">here</a> to set up your account.</p>","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'user_invitation' AND status = 'published'
  AND body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"You have been invited. Click ","format":0},{"type":"link","url":"{{InviteURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to set up your account.","format":0}]}]}}}]'::jsonb;

UPDATE notification_email_templates
SET body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"<p>Hi {{.Name}},</p><p>Someone tried to register an account with your email. If this was you, sign in instead. If not, you can ignore this message.</p>","format":0}]}]}}}]'::jsonb
WHERE notification_type = 'account_exists' AND status = 'published'
  AND body = '[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hi ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":",","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Someone tried to register an account with your email. If this was you, sign in instead. If not, you can ignore this message.","format":0}]}]}}}]'::jsonb;
