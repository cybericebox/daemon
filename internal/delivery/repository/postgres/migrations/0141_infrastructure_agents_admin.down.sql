DROP TABLE IF EXISTS lab_group_placements;
ALTER TABLE infrastructure_agents
    DROP COLUMN IF EXISTS priority,
    DROP COLUMN IF EXISTS enabled,
    DROP COLUMN IF EXISTS ca_pem,
    DROP COLUMN IF EXISTS client_key_ciphertext,
    DROP COLUMN IF EXISTS client_cert_ciphertext,
    DROP COLUMN IF EXISTS endpoint,
    DROP COLUMN IF EXISTS source;
