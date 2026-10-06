-- The platform sending domain becomes an explicit setting (it used to be read
-- off the sender address): Event senders are <tag>@<sending_domain>. Empty =
-- the domain of SMTP_SENDER_EMAIL.
-- The footer is now a rich-text (Lexical) document like the email body blocks.
-- footer_text keeps the older markdown-like footers; they are read as is and
-- replaced the first time the footer is saved.
ALTER TABLE mail_identities
    ADD COLUMN sending_domain text NOT NULL DEFAULT '' CHECK (char_length(sending_domain) <= 253),
    ADD COLUMN footer_content jsonb;
