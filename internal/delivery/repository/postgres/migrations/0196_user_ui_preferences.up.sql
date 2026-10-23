-- A user's own interface settings (the catalog columns and the like): one JSON value per key,
-- gone with the user.
CREATE TABLE user_ui_preferences
(
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key        text        NOT NULL CHECK (key ~ '^[a-z][a-z0-9.-]{0,63}$'),
    value      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
