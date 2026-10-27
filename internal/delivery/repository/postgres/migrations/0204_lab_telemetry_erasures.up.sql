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
    -- When the client had which IP in which lab group (collected before they are blanked), see lab_apply_ip_windows.
    ip_windows         jsonb       NOT NULL DEFAULT '[]'
);

-- The payload of a lab group observation with the IPs of an erased client emptied. windows is a JSON list of
-- {team_id, group, ip, from, to}: the periods in which the IP belonged to the client in a lab group. Inside a
-- window, every JSON string equal to the IP (or the IP with /32) is emptied wherever it is, also in observations
-- that name no client (a policy delta); a window is skipped for an observation that explicitly gives the IP to a
-- client that is not the person (names lists the person's names). Used on the raw rows and the current state; the
-- archive does the same in the application.
CREATE FUNCTION lab_apply_ip_windows(payload jsonb, team uuid, grp text, observed timestamptz, windows jsonb,
                                     names text[]) RETURNS jsonb
    LANGUAGE plpgsql
    STABLE
    PARALLEL SAFE
AS
$$
DECLARE
    doc text := payload::text;
    w   jsonb;
    ip  text;
BEGIN
    IF windows IS NULL OR jsonb_typeof(windows) <> 'array' OR jsonb_array_length(windows) = 0 THEN
        RETURN payload;
    END IF;
    FOR w IN SELECT * FROM jsonb_array_elements(windows)
        LOOP
            CONTINUE WHEN (w ->> 'team_id')::uuid IS DISTINCT FROM team
                OR w ->> 'group' IS DISTINCT FROM grp
                OR observed < (w ->> 'from')::timestamptz
                OR observed > (w ->> 'to')::timestamptz;
            ip := w ->> 'ip';
            IF jsonb_typeof(payload -> 'clients') = 'array' AND EXISTS (SELECT 1
                                                                        FROM jsonb_array_elements(payload -> 'clients') c
                                                                        WHERE jsonb_typeof(c) = 'object'
                                                                          AND c -> 'status' ->> 'assignedIp' = ip
                                                                          AND NOT (COALESCE(c ->> 'name', '') = ANY (names))) THEN
                CONTINUE;
            END IF;
            doc := replace(replace(doc, '"' || ip || '"', '""'), '"' || ip || '/32"', '""');
        END LOOP;
    RETURN doc::jsonb;
END
$$;

-- The purge finds a deleted user's rows by client name.
CREATE INDEX event_vpn_client_minutes_client_idx ON event_vpn_client_minutes (client_name);
CREATE INDEX event_lab_transitions_client_idx ON event_lab_transitions (client_name) WHERE client_name IS NOT NULL;
