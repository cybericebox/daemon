-- Account creation records which documents were shown and accepted instead of a version number: the time stays in
-- tos_accepted_at, the list is new. Rows accepted before this change saw the Terms and the Privacy Policy together.
ALTER TABLE users
    ADD COLUMN legal_documents text[] NOT NULL DEFAULT '{}';

UPDATE users
SET legal_documents = ARRAY['terms', 'privacy']
WHERE tos_accepted_at IS NOT NULL;

ALTER TABLE users
    DROP COLUMN tos_version;
