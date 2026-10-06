-- Admin-configured infrastructure agents. The environment agent keeps its row (source 'env', key
-- 'configured-primary'); an admin agent has source 'admin' and its connection material here: the
-- client certificate and key as ciphertext (the platform secrets key, bound to the agent id), an
-- optional public CA for self-signed development stands. A lower priority is placed first.
ALTER TABLE infrastructure_agents
    ADD COLUMN source                 text    NOT NULL DEFAULT 'env' CHECK (source IN ('env', 'admin')),
    ADD COLUMN endpoint               text    NOT NULL DEFAULT '',
    ADD COLUMN client_cert_ciphertext text    NOT NULL DEFAULT '',
    ADD COLUMN client_key_ciphertext  text    NOT NULL DEFAULT '',
    ADD COLUMN ca_pem                 text    NOT NULL DEFAULT '',
    ADD COLUMN enabled                boolean NOT NULL DEFAULT true,
    ADD COLUMN priority               integer NOT NULL DEFAULT 100;

-- The agent that holds a lab group for its whole life (a team's group, labs and VPN clients come
-- from one cluster). The key is the group name: event teams, the moderators team and the catalog
-- test labs alike. event_id and event_team_id are set for event groups only.
CREATE TABLE lab_group_placements
(
    lab_group_name text        PRIMARY KEY,
    agent_id       uuid        NOT NULL REFERENCES infrastructure_agents (id),
    event_id       uuid        REFERENCES events (id) ON DELETE CASCADE,
    event_team_id  uuid        REFERENCES event_teams (id) ON DELETE CASCADE,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lab_group_placements_agent_idx ON lab_group_placements (agent_id);
CREATE INDEX lab_group_placements_event_idx ON lab_group_placements (event_id);
