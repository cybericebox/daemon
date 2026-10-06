-- One retention rule for unconfirmed accounts (Privacy Policy, "Retention"):
-- an account whose registration was never finished (status 'incomplete':
-- an event or platform invitation, or a self sign-up that never confirmed)
-- is deleted 30 days after it was created, unless it holds a live event
-- invitation. The function runs as one statement, so the batch, the team
-- captaincy hand-over and the deletions commit or fail together.
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
            AND u.created_at < created_before
            -- a live invitation: pending and not dead (finished, withdrawn,
            -- or started while it needed an open roster)
            AND NOT EXISTS (SELECT 1
                            FROM event_participants p
                                     JOIN events e ON e.id = p.event_id
                            WHERE p.user_id = u.id
                              AND p.invited
                              AND p.status = 1
                              AND NOT (e.lifecycle_configured AND (
                                e.withdraw_at <= now_at
                                    OR LEAST(e.manual_finished_at, e.finish_at) <= now_at
                                    OR (e.start_at <= now_at AND (p.invited_to_team OR e.join_policy = 0)))))
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
