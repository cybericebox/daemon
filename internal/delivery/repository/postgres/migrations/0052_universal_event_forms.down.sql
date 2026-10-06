DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM event_forms
        GROUP BY event_id
        HAVING count(*) > 1
    ) OR EXISTS (
        SELECT 1
        FROM event_form_answers answer
        JOIN event_form_versions version ON version.id = answer.form_version_id
        GROUP BY version.event_id, answer.user_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot roll back universal event forms after plural form data exists';
    END IF;
END $$;

DROP TABLE event_form_deliveries;
DROP TABLE event_form_assignments;

ALTER TABLE event_form_answers DROP CONSTRAINT event_form_answers_pkey;
ALTER TABLE event_form_answers ADD CONSTRAINT event_form_answers_pkey PRIMARY KEY (event_id, user_id);

ALTER TABLE event_form_versions DROP CONSTRAINT event_form_versions_form_id_version_key;
ALTER TABLE event_form_versions ADD CONSTRAINT event_form_versions_event_id_version_key UNIQUE (event_id, version);
ALTER TABLE event_form_versions DROP COLUMN form_id;

DROP TABLE event_forms;

