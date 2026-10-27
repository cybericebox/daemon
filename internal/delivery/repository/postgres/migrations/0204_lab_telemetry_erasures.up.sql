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
    erase_ip           boolean     NOT NULL DEFAULT false,
    -- The IPs of the client found in the telemetry (collected before they are blanked) that erasure on request
    -- removes wherever they appear.
    ips                text[]      NOT NULL DEFAULT '{}'
);

-- Whether the payload holds, as a whole JSON string, one of the IPs (or the same with a /32 mask).
CREATE FUNCTION lab_payload_has_ip(payload jsonb, ips text[]) RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
AS
$$
SELECT EXISTS (SELECT 1
               FROM unnest(ips) ip
               WHERE position('"' || ip || '"' IN payload::text) > 0
                  OR position('"' || ip || '/32"' IN payload::text) > 0)
$$;

-- The payload with every JSON string equal to one of the IPs (or the same with a /32 mask) emptied, wherever it is.
CREATE FUNCTION lab_blank_ips(payload jsonb, ips text[]) RETURNS jsonb
    LANGUAGE plpgsql
    IMMUTABLE
    PARALLEL SAFE
AS
$$
DECLARE
    doc text := payload::text;
    ip  text;
BEGIN
    IF COALESCE(cardinality(ips), 0) = 0 THEN
        RETURN payload;
    END IF;
    FOREACH ip IN ARRAY ips
        LOOP
            doc := replace(replace(doc, '"' || ip || '"', '""'), '"' || ip || '/32"', '""');
        END LOOP;
    RETURN doc::jsonb;
END
$$;

-- The purge finds a deleted user's rows by client name.
CREATE INDEX event_vpn_client_minutes_client_idx ON event_vpn_client_minutes (client_name);
CREATE INDEX event_lab_transitions_client_idx ON event_lab_transitions (client_name) WHERE client_name IS NOT NULL;
