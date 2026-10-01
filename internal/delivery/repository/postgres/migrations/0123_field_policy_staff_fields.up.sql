-- A required field added while people already answered: the policy of a form
-- version (or of the team fields) says whether existing participants must fill
-- it too, and whether that also blocks their solution submissions.
ALTER TABLE event_form_versions
    ADD COLUMN require_existing boolean NOT NULL DEFAULT false,
    ADD COLUMN block_submissions boolean NOT NULL DEFAULT false;

ALTER TABLE event_team_field_configs
    ADD COLUMN require_existing boolean NOT NULL DEFAULT false,
    ADD COLUMN block_submissions boolean NOT NULL DEFAULT false;

-- Denormalized count of required fields a participant or a team has not
-- filled (0 = complete or not asked); it drives the «не заповнено» table
-- filter and the submission block. The use case recomputes it whenever
-- answers or the form change.
ALTER TABLE event_participants ADD COLUMN fields_missing integer NOT NULL DEFAULT 0;
ALTER TABLE event_teams ADD COLUMN fields_missing integer NOT NULL DEFAULT 0;

-- Who changed the staff-only fields of a participant or a team, and when.
CREATE TABLE event_staff_field_changes (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('participant', 'team')),
    subject_id uuid NOT NULL,
    actor_id uuid REFERENCES users (id) ON DELETE SET NULL,
    field_keys text[] NOT NULL,
    changed_at timestamptz NOT NULL
);

CREATE INDEX event_staff_field_changes_subject_idx
    ON event_staff_field_changes (event_id, scope, subject_id, changed_at DESC);
