ALTER TABLE event_forms
    ADD COLUMN purpose text NOT NULL DEFAULT 'other'
    CHECK (purpose IN ('registration', 'other'));

-- Older events have one registration form; if the legacy editor created several,
-- its latest form is the one currently visible to participants.
UPDATE event_forms
SET purpose = 'registration'
WHERE id IN (
    SELECT DISTINCT ON (event_id) id
    FROM event_forms
    WHERE title = 'Registration form'
    ORDER BY event_id, created_at DESC, id DESC
);

CREATE UNIQUE INDEX event_forms_registration_event_idx
    ON event_forms (event_id) WHERE purpose = 'registration';
