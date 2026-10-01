CREATE TABLE event_forms (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    title text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    required boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

-- Preserve a backend-generated UUIDv7 from the earliest legacy version as the
-- stable form ID. This migration intentionally contains no UUID generator.
INSERT INTO event_forms (id, event_id, title, enabled, required, created_at, updated_at)
SELECT first_version.id, first_version.event_id, 'Registration form',
       latest_version.enabled, latest_version.required,
       first_version.created_at, latest_version.created_at
FROM (
    SELECT DISTINCT ON (event_id) id, event_id, created_at
    FROM event_form_versions
    ORDER BY event_id, version ASC, id ASC
) AS first_version
JOIN LATERAL (
    SELECT enabled, required, created_at
    FROM event_form_versions
    WHERE event_id = first_version.event_id
    ORDER BY version DESC, id DESC
    LIMIT 1
) AS latest_version ON true;

ALTER TABLE event_form_versions ADD COLUMN form_id uuid REFERENCES event_forms(id) ON DELETE CASCADE;

UPDATE event_form_versions version
SET form_id = form.id
FROM event_forms form
WHERE form.event_id = version.event_id;

ALTER TABLE event_form_versions ALTER COLUMN form_id SET NOT NULL;
ALTER TABLE event_form_versions DROP CONSTRAINT event_form_versions_event_id_version_key;
ALTER TABLE event_form_versions ADD CONSTRAINT event_form_versions_form_id_version_key UNIQUE (form_id, version);

ALTER TABLE event_form_answers DROP CONSTRAINT event_form_answers_pkey;
ALTER TABLE event_form_answers ADD CONSTRAINT event_form_answers_pkey PRIMARY KEY (form_version_id, user_id);

CREATE TABLE event_form_assignments (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    form_id uuid NOT NULL REFERENCES event_forms(id) ON DELETE CASCADE,
    trigger text NOT NULL,
    audience jsonb NOT NULL,
    include_future_participants boolean NOT NULL DEFAULT false,
    presentation text NOT NULL,
    dismissible boolean NOT NULL DEFAULT true,
    gates jsonb NOT NULL DEFAULT '[]'::jsonb,
    at timestamptz,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (trigger IN ('registration_open_completed', 'registration_approval_submitted', 'event_finished', 'at_time', 'manual')),
    CHECK (presentation IN ('modal', 'banner', 'task')),
    CHECK ((trigger = 'at_time') = (at IS NOT NULL))
);

CREATE INDEX event_form_assignments_event_trigger_idx
    ON event_form_assignments (event_id, trigger)
    WHERE enabled;

CREATE TABLE event_form_deliveries (
    form_version_id uuid NOT NULL REFERENCES event_form_versions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    assignment_id uuid NOT NULL REFERENCES event_form_assignments(id) ON DELETE CASCADE,
    presentation text NOT NULL,
    dismissible boolean NOT NULL DEFAULT true,
    gates jsonb NOT NULL DEFAULT '[]'::jsonb,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (form_version_id, user_id),
    CHECK (presentation IN ('modal', 'banner', 'task'))
);

CREATE INDEX event_form_deliveries_pending_user_idx
    ON event_form_deliveries (user_id, created_at DESC)
    WHERE completed_at IS NULL;

