-- When a participant was last online on an event: one row per event x user,
-- touched (throttled to once a minute) by authenticated requests to the event
-- participant API. A time only: no network address, no user agent.
CREATE TABLE event_participant_presence
(
    event_id     uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    last_seen_at timestamptz NOT NULL,
    PRIMARY KEY (event_id, user_id)
);

CREATE INDEX event_participant_presence_user_idx ON event_participant_presence (user_id);

-- The last time a participant touched a laboratory of the event over any
-- surface: the later of the lab access counters (VPN or proxy) and the end of
-- the latest VPN session. NULL when never.
CREATE FUNCTION event_user_last_lab_at(p_event_id uuid, p_user_id uuid)
    RETURNS timestamptz
    LANGUAGE sql
    STABLE
AS
$$
SELECT GREATEST(
               (SELECT max(t.last_seen_at) FROM event_lab_touches t WHERE t.event_id = p_event_id AND t.user_id = p_user_id),
               (SELECT max(s.ended_at) FROM event_vpn_sessions s WHERE s.event_id = p_event_id AND s.user_id = p_user_id))
$$;
