ALTER TABLE users
    ADD COLUMN tos_version int;

UPDATE users
SET tos_version = 1
WHERE tos_accepted_at IS NOT NULL;

ALTER TABLE users
    DROP COLUMN legal_documents;
