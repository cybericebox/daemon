CREATE TABLE event_form_versions (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version > 0),
    enabled boolean NOT NULL DEFAULT true,
    required boolean NOT NULL DEFAULT false,
    document jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (event_id, version)
);

CREATE TABLE event_form_answers (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    form_version_id uuid NOT NULL REFERENCES event_form_versions(id) ON DELETE RESTRICT,
    answers jsonb NOT NULL,
    submitted_at timestamptz NOT NULL,
    PRIMARY KEY (event_id, user_id)
);

CREATE INDEX event_form_answers_version_idx ON event_form_answers (event_id, form_version_id, submitted_at);
