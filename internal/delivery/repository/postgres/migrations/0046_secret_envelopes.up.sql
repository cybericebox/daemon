CREATE TABLE secret_envelopes (
    id UUID PRIMARY KEY,
    purpose TEXT NOT NULL,
    key_version INTEGER NOT NULL CHECK (key_version > 0),
    signal_type TEXT NOT NULL,
    field_path TEXT NOT NULL,
    scope_event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    recipient_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    ciphertext TEXT NOT NULL,
    wrapped_data_key TEXT NOT NULL,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX secret_envelopes_scope_idx ON secret_envelopes (scope_event_id, created_at);
