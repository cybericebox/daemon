-- Agents are enrolled, not uploaded: the platform generates its client key and a signing key pair for
-- the lab access tokens, the agent signs the client certificate (CN = the tenant name) and stores
-- the access public key. The certificate is public (plain); both private keys are ciphertext
-- bound to the agent id. The tenant name always comes from the certificate CN.
--
-- Admin rows of the earlier certificate-upload layout cannot be used (no keys we generated), so they
-- go; the environment agent row stays.
DELETE FROM lab_group_placements
WHERE agent_id IN (SELECT id FROM infrastructure_agents WHERE source = 'admin');
DELETE FROM infrastructure_agents WHERE source = 'admin';

ALTER TABLE infrastructure_agents RENAME COLUMN client_cert_ciphertext TO client_cert_pem;
ALTER TABLE infrastructure_agents
    ADD COLUMN tenant                         text        NOT NULL DEFAULT '',
    ADD COLUMN cert_not_after                 timestamptz,
    ADD COLUMN access_key_id                  text        NOT NULL DEFAULT '',
    ADD COLUMN access_private_key_ciphertext  text        NOT NULL DEFAULT '',
    ADD COLUMN access_public_key              text        NOT NULL DEFAULT '',
    -- [{"key_id": "...", "retired_at": "<RFC 3339>"}]: rotated-out keys wait for RemoveAccessKey until
    -- the longest token signed with them has expired.
    ADD COLUMN retired_access_keys            jsonb       NOT NULL DEFAULT '[]';

-- The same agent cannot be added twice. The tenant is not unique: other clusters may reuse a name.
CREATE UNIQUE INDEX infrastructure_agents_admin_endpoint_idx
    ON infrastructure_agents (endpoint) WHERE source = 'admin';
