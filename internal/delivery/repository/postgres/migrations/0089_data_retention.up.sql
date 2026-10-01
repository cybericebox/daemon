-- Data retention (Privacy Policy, section "Retention").
--
-- inactivity_warned_at: when the inactive-account warning email was queued;
-- cleared when the user signs in again (last_seen passes it).
-- personal_data_purged_at: when the residual personal data of a deleted
-- account was removed; the tombstone row stays as the anonymized placeholder
-- that competition results keep referencing.
ALTER TABLE users
    ADD COLUMN inactivity_warned_at    timestamptz,
    ADD COLUMN personal_data_purged_at timestamptz;

CREATE INDEX users_inactivity_idx
    ON users (last_seen, id)
    WHERE deleted_at IS NULL;
CREATE INDEX users_inactivity_warned_idx
    ON users (inactivity_warned_at, id)
    WHERE deleted_at IS NULL AND inactivity_warned_at IS NOT NULL;
CREATE INDEX users_personal_data_purge_idx
    ON users (deleted_at, id)
    WHERE deleted_at IS NOT NULL AND personal_data_purged_at IS NULL;

-- Time-based purges.
CREATE INDEX event_lab_observations_observed_idx
    ON event_lab_observations (observed_at);

-- Per-user purges of deleted accounts.
CREATE INDEX event_form_answers_user_idx ON event_form_answers (user_id);
CREATE INDEX event_form_deliveries_user_idx ON event_form_deliveries (user_id);
CREATE INDEX notification_dispatches_recipient_idx ON notification_dispatches (recipient_user_id);
CREATE INDEX secret_envelopes_recipient_idx
    ON secret_envelopes (recipient_user_id)
    WHERE recipient_user_id IS NOT NULL;

-- Inactive-account warning: a security email, always on.
INSERT INTO notification_settings (notification_type, channel, enabled, user_can_change, user_default)
VALUES ('account_inactivity_warning', 'email', true, false, true)
ON CONFLICT (notification_type, channel) DO UPDATE
    SET enabled = EXCLUDED.enabled,
        user_can_change = EXCLUDED.user_can_change,
        user_default = EXCLUDED.user_default;

-- Same shape as the other platform templates (logo, text, button); the
-- standard footer is appended at send time.
INSERT INTO notification_email_templates
    (id, notification_type, status, subject, preheader, body, styling, published_at)
SELECT gen_random_uuid(), 'account_inactivity_warning', 'published',
       'Ваш обліковий запис CyberICEBox буде видалено',
       'Увійдіть, щоб зберегти обліковий запис',
       '[{"type":"logo","align":"center","width_px":64},{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Вітаємо, ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":"!","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Ви давно не входили до свого облікового запису CyberICEBox. Відповідно до Політики конфіденційності ми видалимо обліковий запис і пов’язані з ним персональні дані ","format":0},{"type":"variable","varName":"DeletionDate"},{"type":"text","text":".","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Щоб зберегти обліковий запис, увійдіть до нього до цієї дати. Результати змагань залишаться в історії заходів у знеособленому вигляді.","format":0}]}]}}},{"type":"button","label":"Увійти","url":"{{SignInURL}}","align":"left"}]'::jsonb,
       styling, now()
FROM notification_email_templates
WHERE notification_type = 'password_reset'
  AND scope_event_id IS NULL
  AND status = 'published'
LIMIT 1;
