CREATE TABLE admin_audit_log (
    id uuid PRIMARY KEY,
    actor_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    permission text NOT NULL,
    method text NOT NULL,
    route text NOT NULL,
    response_status integer NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE INDEX admin_audit_log_created_at_idx ON admin_audit_log (created_at DESC, id DESC);
CREATE INDEX admin_audit_log_actor_id_idx ON admin_audit_log (actor_id, created_at DESC);
