CREATE TABLE event_lab_access_syncs
(
    event_team_id    uuid        PRIMARY KEY REFERENCES event_teams (id) ON DELETE CASCADE,
    desired_revision bigint      NOT NULL DEFAULT 1 CHECK (desired_revision > 0),
    applied_revision bigint      NOT NULL DEFAULT 0 CHECK (applied_revision >= 0 AND applied_revision <= desired_revision),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX event_lab_access_syncs_dirty_idx
    ON event_lab_access_syncs (updated_at, event_team_id)
    WHERE desired_revision > applied_revision;
