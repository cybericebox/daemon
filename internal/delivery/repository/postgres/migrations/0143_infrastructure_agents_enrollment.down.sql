DROP INDEX IF EXISTS infrastructure_agents_admin_endpoint_idx;
ALTER TABLE infrastructure_agents
    DROP COLUMN IF EXISTS retired_access_keys,
    DROP COLUMN IF EXISTS access_public_key,
    DROP COLUMN IF EXISTS access_private_key_ciphertext,
    DROP COLUMN IF EXISTS access_key_id,
    DROP COLUMN IF EXISTS cert_not_after,
    DROP COLUMN IF EXISTS tenant;
ALTER TABLE infrastructure_agents RENAME COLUMN client_cert_pem TO client_cert_ciphertext;
