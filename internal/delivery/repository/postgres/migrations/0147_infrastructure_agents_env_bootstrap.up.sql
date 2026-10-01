-- The environment agent is no longer a projection of deployment config: AGENT_ENDPOINT with a one-time
-- enrollment token only bootstraps a normal enrolled agent row (source 'env'), kept in the database like
-- an admin agent, with its keys encrypted and its certificate renewed automatically. The old projection
-- row (no endpoint, no keys) goes.
DELETE FROM lab_group_placements
WHERE agent_id IN (SELECT id FROM infrastructure_agents WHERE key = 'configured-primary');
DELETE FROM infrastructure_agents WHERE key = 'configured-primary';

-- One live agent per endpoint, whichever way it was added.
DROP INDEX infrastructure_agents_admin_endpoint_idx;
CREATE UNIQUE INDEX infrastructure_agents_endpoint_idx
    ON infrastructure_agents (endpoint) WHERE endpoint <> '' AND archived_at IS NULL;
