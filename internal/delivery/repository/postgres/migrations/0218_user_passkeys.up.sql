-- Passkeys (WebAuthn discoverable credentials with user verification): a sign-in method of its own.
-- The row holds the public half only; no biometric data ever reaches the server. sign_count is the
-- authenticator's counter (a regression is a clone signal); the flags tell a device-bound key from a synced one.
CREATE TABLE user_passkeys
(
    id              uuid PRIMARY KEY,
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id   bytea       NOT NULL,
    public_key      bytea       NOT NULL,
    sign_count      bigint      NOT NULL DEFAULT 0,
    aaguid          bytea       NOT NULL DEFAULT ''::bytea,
    transports      text[]      NOT NULL DEFAULT '{}',
    user_verified   boolean     NOT NULL DEFAULT true,
    backup_eligible boolean     NOT NULL DEFAULT false,
    backup_state    boolean     NOT NULL DEFAULT false,
    name            text        NOT NULL,
    created_at      timestamptz NOT NULL,
    last_used_at    timestamptz,
    -- set when the counter went backwards: a copy of the key exists. The key can never sign in again.
    compromised_at  timestamptz
);

CREATE UNIQUE INDEX user_passkeys_credential_uidx ON user_passkeys (credential_id);
CREATE INDEX user_passkeys_user_idx ON user_passkeys (user_id);
