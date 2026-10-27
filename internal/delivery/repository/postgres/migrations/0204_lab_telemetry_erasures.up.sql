-- Deleting an account must take the person out of the lab telemetry too.
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

-- Accounts erased from the lab telemetry. Only the link to the person goes: the
-- VPN client name "p-<id>" becomes a pseudonym, the same for that client in every
-- table (raw observations, current state, archive, minutes, transitions,
-- journal states), so the statistics stay whole and different people are never
-- merged. The assigned IPs stay. The purge of a deleted account queues it with
-- the pseudonym and the events it took part in; the retention job keeps
-- rewriting what the telemetry still brings in with the real name (raw rows that
-- arrive or get archived later) until the entry has settled, then removes it:
-- the link between the person and the pseudonym does not outlive the cleanup.
CREATE TABLE lab_telemetry_erasures
(
    user_id            uuid PRIMARY KEY,
    client_name        text        NOT NULL,
    pseudonym          text        NOT NULL,
    event_ids          uuid[]      NOT NULL,
    queued_at          timestamptz NOT NULL DEFAULT now(),
    -- Erasure on request (an objection) also blanks the IP assigned to the client; the purge of a deleted account
    -- leaves the IPs, they are internal addresses the statistics need.
    erase_ip           boolean     NOT NULL DEFAULT false
);

-- Whether a lab group payload lists a client with this name and a non-empty assigned IP.
CREATE FUNCTION lab_client_has_ip(payload jsonb, client_name text) RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
AS
$$
SELECT CASE
           WHEN jsonb_typeof(payload -> 'clients') = 'array' THEN EXISTS (SELECT 1
                                                                          FROM jsonb_array_elements(payload -> 'clients') c
                                                                          WHERE jsonb_typeof(c) = 'object'
                                                                            AND c ->> 'name' = client_name
                                                                            AND COALESCE(c -> 'status' ->> 'assignedIp', '') <> '')
           ELSE false END
$$;

-- The payload with the assigned IP of the named client blanked.
CREATE FUNCTION lab_blank_client_ip(payload jsonb, client_name text) RETURNS jsonb
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
AS
$$
SELECT CASE
           WHEN lab_client_has_ip(payload, client_name) THEN
               jsonb_set(payload, '{clients}',
                         (SELECT jsonb_agg(CASE
                                               WHEN jsonb_typeof(c) = 'object' AND c ->> 'name' = client_name
                                                   THEN jsonb_set(c, '{status}', COALESCE(c -> 'status', '{}'::jsonb) || '{"assignedIp":""}'::jsonb)
                                               ELSE c END)
                          FROM jsonb_array_elements(payload -> 'clients') c))
           ELSE payload END
$$;

-- The purge finds a deleted user's rows by client name.
CREATE INDEX event_vpn_client_minutes_client_idx ON event_vpn_client_minutes (client_name);
CREATE INDEX event_lab_transitions_client_idx ON event_lab_transitions (client_name) WHERE client_name IS NOT NULL;
