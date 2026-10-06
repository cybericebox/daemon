CREATE TABLE event_team_field_configs (
    event_id uuid PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version > 0),
    enabled boolean NOT NULL DEFAULT false,
    required boolean NOT NULL DEFAULT false,
    document jsonb NOT NULL,
    updated_at timestamptz NOT NULL
);

ALTER TABLE event_teams
    ADD COLUMN extra_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT event_teams_extra_fields_object CHECK (jsonb_typeof(extra_fields) = 'object');
