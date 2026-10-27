-- Deleting an account must take the participant out of the lab telemetry too.
-- The telemetry names a participant by the name of the VPN client, "p-" and the
-- user id as 25 base36 characters (labBindingModel.ParticipantClientName), and
-- the raw observations keep that name and the client's assigned IP.

-- The client name of a user, the same encoding as labBindingModel.ShortID.
CREATE FUNCTION lab_client_name(user_id uuid) RETURNS text
    LANGUAGE plpgsql
    IMMUTABLE
    PARALLEL SAFE
AS
$$
DECLARE
    bytes bytea   := uuid_send(user_id);
    n     numeric := 0;
    out   text    := '';
BEGIN
    FOR i IN 0..15
        LOOP
            n := n * 256 + get_byte(bytes, i);
        END LOOP;
    FOR i IN 1..25
        LOOP
            out := substr('0123456789abcdefghijklmnopqrstuvwxyz', (n % 36)::int + 1, 1) || out;
            n := div(n, 36);
        END LOOP;
    RETURN 'p-' || out;
END
$$;

-- Accounts whose archived telemetry is still to be rewritten. The purge of a
-- deleted account queues it (with the events it took part in) and the retention
-- job scrubs the archive chunks of those events, then removes the entry. It
-- holds a client name only until then.
CREATE TABLE lab_telemetry_erasures
(
    user_id     uuid PRIMARY KEY,
    client_name text        NOT NULL,
    event_ids   uuid[]      NOT NULL,
    queued_at   timestamptz NOT NULL DEFAULT now()
);

-- The purge finds a deleted user's rows by client name.
CREATE INDEX event_vpn_client_minutes_client_idx ON event_vpn_client_minutes (client_name);
CREATE INDEX event_lab_transitions_client_idx ON event_lab_transitions (client_name) WHERE client_name IS NOT NULL;
