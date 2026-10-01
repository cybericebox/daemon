-- A team is «formed» when its roster is closed for good. Only a formed team gets
-- tasks, labs, stand and VPN/proxy access. formed_by NULL = formed without a
-- person (individual mode, the moderators team, the backfill).
-- An event without late join forms every team at its start; that is computed on
-- read by event_team_formed and never stored, so it has no lag.
ALTER TABLE event_teams
    ADD COLUMN formed_at timestamptz,
    ADD COLUMN formed_by uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD CONSTRAINT event_teams_formed_by_check CHECK (formed_by IS NULL OR formed_at IS NOT NULL);

-- Solo teams and the hidden moderators team are formed when created.
UPDATE event_teams SET formed_at = created_at WHERE individual OR moderators;

-- Teams of events that already started keep working: they were locked at the
-- start or, for rolling events, already had tasks.
UPDATE event_teams team
SET formed_at = GREATEST(team.created_at, event.start_at)
FROM events event
WHERE event.id = team.event_id
  AND team.formed_at IS NULL
  AND event.lifecycle_configured
  AND event.start_at <= now();

CREATE INDEX event_teams_unformed_idx ON event_teams (event_id) WHERE formed_at IS NULL;

-- The one formation predicate: an explicit formation, or the start of an event
-- that does not accept late joiners (join_policy 0 = locked at start).
CREATE FUNCTION event_team_formed(p_event_id uuid, p_formed_at timestamptz)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT p_formed_at IS NOT NULL
           OR EXISTS (SELECT 1
                      FROM events event
                      WHERE event.id = p_event_id
                        AND event.lifecycle_configured
                        AND event.join_policy = 0
                        AND event.start_at <= now())
$$;

-- A stand is prepared for a formed team, and ahead of time for every team of an
-- event without late join (they all form at its start). With late join a stand
-- waits for the captain's confirmation.
CREATE FUNCTION event_team_stand_wanted(p_event_id uuid, p_formed_at timestamptz)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT p_formed_at IS NOT NULL
           OR EXISTS (SELECT 1 FROM events event WHERE event.id = p_event_id AND event.join_policy = 0)
$$;

-- The name of a team's LabGroup: e-<event>-t-<team>, both ids as the 25-character
-- base36 the laboratory operator uses for lab ids (56 characters, inside the 63
-- of a Kubernetes label). Go builds the same name (labBindingModel.GroupName).
CREATE FUNCTION lab_short_id(p_id uuid)
    RETURNS text
    LANGUAGE plpgsql
    IMMUTABLE
AS
$$
DECLARE
    hex    text    := replace(p_id::text, '-', '');
    n      numeric := 0;
    digits text    := '0123456789abcdefghijklmnopqrstuvwxyz';
    out    text    := '';
    d      integer;
BEGIN
    FOR i IN 0..3 LOOP
        n := n * 4294967296 + (('x' || substr(hex, i * 8 + 1, 8))::bit(32)::bigint)::numeric;
    END LOOP;
    FOR i IN 1..25 LOOP
        d := (n % 36)::integer;
        out := substr(digits, d + 1, 1) || out;
        n := div(n, 36);
    END LOOP;
    RETURN out;
END
$$;

CREATE FUNCTION lab_group_name(p_event_id uuid, p_team_id uuid)
    RETURNS text
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT 'e-' || lab_short_id(p_event_id) || '-t-' || lab_short_id(p_team_id)
$$;
