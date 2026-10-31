CREATE OR REPLACE FUNCTION event_team_stand_wanted(p_event_id uuid, p_formed_at timestamptz)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT p_formed_at IS NOT NULL
           OR EXISTS (SELECT 1 FROM events event WHERE event.id = p_event_id AND event.join_policy = 0)
$$;
