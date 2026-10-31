-- A stand is prepared only for a formed team. In an event that closes joining at its start the teams form by
-- themselves at the start; until then an unformed team generates nothing, and from the start its stand is
-- deployed like a late member of the same group.
CREATE OR REPLACE FUNCTION event_team_stand_wanted(p_event_id uuid, p_formed_at timestamptz)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT event_team_formed(p_event_id, p_formed_at)
$$;
