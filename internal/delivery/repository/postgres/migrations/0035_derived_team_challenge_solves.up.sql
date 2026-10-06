-- A solved challenge is a projection of immutable submissions and their
-- append-only review decisions. It must not be stored independently: a later
-- rejection can make a different, earlier or later attempt the first solve.
CREATE VIEW effective_challenge_attempts AS
SELECT ca.id,
       ca.event_id,
       ca.event_team_id,
       ca.team_challenge_id,
       ca.user_id,
       ca.answer,
       ca.correct AS automatic_correct,
       ca.received_at,
       ca.created_at,
       COALESCE(latest.id, '00000000-0000-0000-0000-000000000000'::uuid) AS decision_id,
       COALESCE(latest.decision, 0) AS decision,
       COALESCE(latest.reason, '')::text AS decision_reason,
       COALESCE(latest.decided_by, '00000000-0000-0000-0000-000000000000'::uuid) AS decided_by,
       COALESCE(latest.decided_at, 'epoch'::timestamptz) AS decided_at,
       CASE COALESCE(latest.decision, 0)
           WHEN 1 THEN TRUE
           WHEN 2 THEN FALSE
           ELSE ca.correct
       END::boolean AS effective_correct
FROM challenge_attempts ca
LEFT JOIN LATERAL (
    SELECT cad.id, cad.decision, cad.reason, cad.decided_by, cad.decided_at
    FROM challenge_attempt_decisions cad
    WHERE cad.challenge_attempt_id = ca.id
    ORDER BY cad.decided_at DESC, cad.id DESC
    LIMIT 1
) latest ON TRUE;

-- This projection intentionally contains only solved tasks. It is a
-- rebuildable cache for scoreboard and prerequisite reads, not a source of
-- truth. Its compact time index therefore never includes unsolved tasks.
CREATE TABLE team_challenge_solves
(
    team_challenge_id uuid        PRIMARY KEY REFERENCES team_challenges (id) ON DELETE CASCADE,
    solved_at         timestamptz NOT NULL
);

CREATE INDEX team_challenge_solves_time_idx
    ON team_challenge_solves (solved_at ASC, team_challenge_id ASC);

-- Preserve current competitions while making the projection rebuildable from
-- the only source of truth: historical attempts and their latest decisions.
INSERT INTO team_challenge_solves (team_challenge_id, solved_at)
SELECT team_challenge_id, min(received_at)
FROM effective_challenge_attempts
WHERE effective_correct
GROUP BY team_challenge_id;

ALTER TABLE team_challenges DROP COLUMN solved_at;
