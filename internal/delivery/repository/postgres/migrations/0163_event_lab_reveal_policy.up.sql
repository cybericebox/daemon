ALTER TABLE event_team_labs ADD COLUMN definition_version_id uuid,ADD COLUMN definition_hash text NOT NULL DEFAULT '' CHECK(definition_hash='' OR definition_hash ~ '^[0-9a-f]{64}$');
ALTER TABLE event_team_labs ADD COLUMN retention_minutes integer NOT NULL DEFAULT 60 CHECK(retention_minutes BETWEEN 0 AND 10080);
UPDATE event_team_labs l SET retention_minutes=COALESCE((c.lab_policy->>'RetentionMinutes')::integer,c.stand_teardown_delay_minutes,60)
FROM event_configs c WHERE c.event_id=l.event_id;
CREATE TABLE event_lab_reveal_barriers (
 event_id uuid NOT NULL,
 event_exercise_id uuid PRIMARY KEY,
 revision bigint NOT NULL CHECK(revision>0),
 mode text NOT NULL CHECK(mode IN ('all_ready','as_ready')),
 eligible_team_ids uuid[] NOT NULL,
 opened_at timestamptz,
 created_at timestamptz NOT NULL
);
