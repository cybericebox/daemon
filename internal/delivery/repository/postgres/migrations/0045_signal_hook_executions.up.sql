CREATE TABLE signal_hook_executions (
    signal_id UUID NOT NULL REFERENCES signal_outbox(id) ON DELETE CASCADE,
    hook_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (signal_id, hook_name)
);

CREATE INDEX signal_hook_executions_pending_idx
    ON signal_hook_executions (available_at, signal_id)
    WHERE status = 'pending';
