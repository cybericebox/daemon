-- Unconfirmed accounts: the 30-day clock runs from creation or from the last
-- (re)sent invitation, whichever is later; a live invitation no longer keeps
-- an account on its own (setup links last 7 days, well under 30).
-- users.invitation_sent_at is a narrow column (outside the user aggregate's
-- UPDATE set, like last_seen), written whenever an event or platform
-- invitation email is queued.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS invitation_sent_at timestamptz;

UPDATE users u
SET invitation_sent_at = sent.at
FROM (SELECT user_id, max(invitation_sent_at) AS at
      FROM event_participants
      WHERE invitation_sent_at IS NOT NULL
      GROUP BY user_id) sent
WHERE u.id = sent.user_id
  AND u.status = 'incomplete';

CREATE OR REPLACE FUNCTION purge_unconfirmed_accounts(created_before timestamptz, now_at timestamptz, batch_size integer)
    RETURNS integer
    LANGUAGE plpgsql AS
$$
DECLARE
    ids    uuid[];
    purged integer;
BEGIN
    SELECT array_agg(batch.id)
    INTO ids
    FROM (SELECT u.id
          FROM users u
          WHERE u.status = 'incomplete'
            AND u.deleted_at IS NULL
            -- the clock runs from creation or the last (re)sent invitation
            AND GREATEST(u.created_at, COALESCE(u.invitation_sent_at, u.created_at)) < created_before
            -- never true for an account that never signed in; kept so a
            -- restricting reference can never fail the whole batch
            AND NOT EXISTS (SELECT 1 FROM challenge_attempts a WHERE a.user_id = u.id)
            AND NOT EXISTS (SELECT 1 FROM challenge_attempt_decisions d WHERE d.decided_by = u.id)
            AND NOT EXISTS (SELECT 1 FROM admin_audit_log l WHERE l.actor_id = u.id)
            AND NOT EXISTS (SELECT 1 FROM exercise_test_deployments x WHERE x.created_by = u.id)
          ORDER BY u.created_at, u.id
          LIMIT batch_size FOR UPDATE SKIP LOCKED) batch;
    IF ids IS NULL THEN
        RETURN 0;
    END IF;

    -- A team led by a removed account: the first confirmed member becomes
    -- captain, else the first remaining invitee (captain on accepting), else
    -- the empty team goes.
    WITH successor AS (SELECT DISTINCT ON (t.id) t.id AS team_id, p.user_id
                       FROM event_teams t
                                JOIN event_participants p ON p.team_id = t.id
                       WHERE t.captain_id = ANY (ids)
                         AND NOT p.user_id = ANY (ids)
                       ORDER BY t.id, p.created_at, p.user_id),
         promoted AS (UPDATE event_participants p
             SET team_role = 0
             FROM successor s
             WHERE p.team_id = s.team_id AND p.user_id = s.user_id)
    UPDATE event_teams t
    SET captain_id = s.user_id,
        updated_at = now_at
    FROM successor s
    WHERE t.id = s.team_id;

    UPDATE event_teams t
    SET captain_id = s.user_id,
        updated_at = now_at
    FROM (SELECT DISTINCT ON (p.invited_team_id) p.invited_team_id AS team_id, p.user_id
          FROM event_participants p
                   JOIN event_teams team ON team.id = p.invited_team_id
          WHERE team.captain_id = ANY (ids)
            AND p.invited
            AND p.status = 1
            AND NOT p.user_id = ANY (ids)
          ORDER BY p.invited_team_id, p.created_at, p.user_id) s
    WHERE t.id = s.team_id;

    DELETE FROM event_teams WHERE captain_id = ANY (ids);

    -- The delivery journal and inbox have no foreign key; pending
    -- participations, sessions, providers and the rest cascade.
    DELETE FROM notification_dispatches WHERE recipient_user_id = ANY (ids);
    DELETE FROM in_app_notifications WHERE user_id = ANY (ids);
    DELETE FROM users WHERE id = ANY (ids);
    GET DIAGNOSTICS purged = ROW_COUNT;
    RETURN purged;
END
$$;
