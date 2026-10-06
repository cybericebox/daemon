-- Seed global notification_settings for the transactional auth emails.
--
-- GetActiveChannels resolves a notification's channels from notification_settings
-- (global) joined with notification_user_settings (per-user). With no global rows
-- every type resolves to zero channels, so dispatches are created but produce no
-- targets and no email is ever sent (observed at registration).
--
-- These are security/transactional emails: user_can_change = false makes them
-- always active regardless of per-user preferences (the GetActiveChannels filter
-- `NOT g.user_can_change OR ...` short-circuits to true). enabled = true turns the
-- channel on; user_default = true is the would-be default if it were user-toggleable.
INSERT INTO notification_settings (notification_type, channel, enabled, user_can_change, user_default)
VALUES ('continue_registration', 'email', true, false, true),
       ('password_reset', 'email', true, false, true),
       ('email_confirmation', 'email', true, false, true),
       ('user_invitation', 'email', true, false, true),
       ('account_exists', 'email', true, false, true) ON CONFLICT (notification_type, channel) DO
UPDATE
    SET enabled = EXCLUDED.enabled,
    user_can_change = EXCLUDED.user_can_change,
    user_default = EXCLUDED.user_default;
