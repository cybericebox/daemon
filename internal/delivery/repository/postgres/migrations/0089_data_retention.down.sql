DELETE FROM notification_email_templates WHERE notification_type = 'account_inactivity_warning';
DELETE FROM notification_settings WHERE notification_type = 'account_inactivity_warning';

DROP INDEX IF EXISTS secret_envelopes_recipient_idx;
DROP INDEX IF EXISTS notification_dispatches_recipient_idx;
DROP INDEX IF EXISTS event_form_deliveries_user_idx;
DROP INDEX IF EXISTS event_form_answers_user_idx;
DROP INDEX IF EXISTS event_lab_observations_observed_idx;
DROP INDEX IF EXISTS users_personal_data_purge_idx;
DROP INDEX IF EXISTS users_inactivity_warned_idx;
DROP INDEX IF EXISTS users_inactivity_idx;

ALTER TABLE users
    DROP COLUMN personal_data_purged_at,
    DROP COLUMN inactivity_warned_at;
