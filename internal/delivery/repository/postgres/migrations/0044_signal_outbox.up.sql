CREATE TABLE signal_outbox (
    id uuid PRIMARY KEY,
    signal_type text NOT NULL,
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL,
    claimed_at timestamptz,
    completed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL
);

CREATE INDEX signal_outbox_claim_idx
    ON signal_outbox (status, available_at, occurred_at)
    WHERE status = 'pending';
