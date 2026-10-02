-- name: ListInfrastructureAgents :many
SELECT *
FROM infrastructure_agents
ORDER BY key;

-- name: GetInfrastructureAgent :one
SELECT *
FROM infrastructure_agents
WHERE id = sqlc.arg(id);

-- name: CreateInfrastructureAgent :one
-- An enrolled agent (source admin, or env when bootstrapped from the deployment config): its key is its
-- id; the certificate is public, both private keys are ciphertext.
INSERT INTO infrastructure_agents (id, key, name, configured, source, endpoint, tenant, client_cert_pem, client_key_ciphertext,
                                   cert_not_after, ca_pem, access_key_id, access_private_key_ciphertext, access_public_key,
                                   enabled, priority, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(key), sqlc.arg(name), true, sqlc.arg(source), sqlc.arg(endpoint), sqlc.arg(tenant),
        sqlc.arg(client_cert_pem), sqlc.arg(client_key_ciphertext), sqlc.arg(cert_not_after), sqlc.arg(ca_pem),
        sqlc.arg(access_key_id), sqlc.arg(access_private_key_ciphertext), sqlc.arg(access_public_key),
        sqlc.arg(enabled), sqlc.arg(priority), sqlc.arg(created_at), sqlc.arg(updated_at))
RETURNING *;

-- name: UpdateInfrastructureAgent :execrows
-- What an admin may change after enrollment: the label, the order, the switch and the server CA. The
-- endpoint identifies the agent (and its certificate), so it never changes.
UPDATE infrastructure_agents
SET name       = sqlc.arg(name),
    ca_pem     = sqlc.arg(ca_pem),
    enabled    = sqlc.arg(enabled),
    priority   = sqlc.arg(priority),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetInfrastructureAgentCertificate :execrows
-- A renewed client certificate with its new key (the tenant is the certificate CN and stays).
UPDATE infrastructure_agents
SET client_cert_pem       = sqlc.arg(client_cert_pem),
    client_key_ciphertext = sqlc.arg(client_key_ciphertext),
    cert_not_after        = sqlc.arg(cert_not_after),
    tenant                = sqlc.arg(tenant),
    updated_at            = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetInfrastructureAgentAccessKey :execrows
-- A rotated access key: the new key signs from now on, the previous ones are recorded as retired.
UPDATE infrastructure_agents
SET access_key_id                 = sqlc.arg(access_key_id),
    access_private_key_ciphertext = sqlc.arg(access_private_key_ciphertext),
    access_public_key             = sqlc.arg(access_public_key),
    retired_access_keys           = sqlc.arg(retired_access_keys),
    updated_at                    = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetInfrastructureAgentRetiredKeys :execrows
-- The retired keys that are still waiting for RemoveAccessKey.
UPDATE infrastructure_agents
SET retired_access_keys = sqlc.arg(retired_access_keys),
    updated_at          = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteInfrastructureAgent :execrows
DELETE FROM infrastructure_agents
WHERE id = sqlc.arg(id);

-- name: ClaimLabGroupPlacement :one
-- Places a group on an agent unless it is placed already; returns the agent that holds it, so two
-- racing callers agree on the winner.
INSERT INTO lab_group_placements (lab_group_name, agent_id, event_id, event_team_id, created_at)
VALUES (sqlc.arg(lab_group_name), sqlc.arg(agent_id), sqlc.narg(event_id), sqlc.narg(event_team_id), sqlc.arg(created_at))
ON CONFLICT (lab_group_name) DO UPDATE SET lab_group_name = lab_group_placements.lab_group_name
RETURNING agent_id;

-- name: GetLabGroupPlacement :one
SELECT agent_id
FROM lab_group_placements
WHERE lab_group_name = sqlc.arg(lab_group_name);

-- name: DeleteLabGroupPlacement :exec
DELETE FROM lab_group_placements
WHERE lab_group_name = sqlc.arg(lab_group_name);

-- name: CountLabGroupPlacementsByAgent :many
SELECT agent_id, count(*)::bigint AS groups
FROM lab_group_placements
GROUP BY agent_id;

-- name: SetInfrastructureAgentCapacity :execrows
-- The last known capacity (the tenant quota; NULL = no limit on that resource) and when it was read.
UPDATE infrastructure_agents
SET capacity_cpu_millicores = sqlc.narg(capacity_cpu_millicores),
    capacity_memory_bytes   = sqlc.narg(capacity_memory_bytes),
    capacity_seen_at        = sqlc.arg(seen_at)
WHERE id = sqlc.arg(id);

-- name: SetInfrastructureAgentFeatures :execrows
-- The last laboratory features the agent reported (JSON) and when they were read.
UPDATE infrastructure_agents
SET features    = sqlc.arg(features),
    features_at = sqlc.arg(seen_at)
WHERE id = sqlc.arg(id)
  AND archived_at IS NULL;

-- name: ArchiveInfrastructureAgent :execrows
-- Soft delete: the record stays for history, everything that connects to the agent is wiped and the
-- name is freed by the caller's rename.
UPDATE infrastructure_agents
SET name                           = sqlc.arg(name),
    endpoint                       = '',
    tenant                         = '',
    client_cert_pem                = '',
    client_key_ciphertext          = '',
    cert_not_after                 = NULL,
    ca_pem                         = '',
    access_key_id                  = '',
    access_private_key_ciphertext  = '',
    access_public_key              = '',
    retired_access_keys            = '[]',
    features                       = NULL,
    features_at                    = NULL,
    enabled                        = false,
    archived_at                    = sqlc.arg(archived_at),
    updated_at                     = sqlc.arg(archived_at)
WHERE id = sqlc.arg(id)
  AND archived_at IS NULL;

-- name: ReplaceInfrastructureAgentCredentials :execrows
-- Reconnect: the same cluster enrolled again for new keys. The record, its priority and its lab groups stay.
UPDATE infrastructure_agents
SET client_cert_pem                = sqlc.arg(client_cert_pem),
    client_key_ciphertext          = sqlc.arg(client_key_ciphertext),
    cert_not_after                 = sqlc.arg(cert_not_after),
    tenant                         = sqlc.arg(tenant),
    ca_pem                         = sqlc.arg(ca_pem),
    access_key_id                  = sqlc.arg(access_key_id),
    access_private_key_ciphertext  = sqlc.arg(access_private_key_ciphertext),
    access_public_key              = sqlc.arg(access_public_key),
    retired_access_keys            = '[]',
    updated_at                     = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND archived_at IS NULL;
