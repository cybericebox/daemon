-- Editable platform email footer (variables {platform_name}, {site_url},
-- {privacy_url}, {reply_to}). Empty = the built-in default. Only the platform
-- row (scope_event_id IS NULL) carries it; Events cannot edit the footer.
ALTER TABLE mail_identities
    ADD COLUMN footer_text text NOT NULL DEFAULT '' CHECK (char_length(footer_text) <= 1000);
