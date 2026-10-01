-- The moderators team is always hidden. Stands and scoring rely on it, so the
-- database refuses any row that breaks it.
UPDATE event_teams SET hidden = true WHERE moderators AND NOT hidden;

ALTER TABLE event_teams
    ADD CONSTRAINT event_teams_moderators_hidden CHECK (NOT moderators OR hidden);

-- The one visibility predicate of the results: a team competes in the public
-- results, the scoreboard, counters and solver lists only when it is neither
-- hidden nor the moderators team and is admitted. Visibility is applied at
-- read time, never stored in a projection.
CREATE FUNCTION event_team_visible(p_hidden boolean, p_moderators boolean, p_event_id uuid, p_individual boolean,
                                   p_admitted_manually boolean, p_admission_locked boolean, p_member_count integer)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT NOT p_hidden AND NOT p_moderators
           AND event_team_admitted(p_event_id, p_individual, p_admitted_manually, p_admission_locked, p_member_count)
$$;

-- Keyset order of the solvers list of one challenge.
CREATE INDEX team_challenge_solves_order_idx
    ON team_challenge_solves (solved_at, team_challenge_id);

-- Moderators answers are recorded as real attempts, so the dry-run check log
-- is no longer needed.
DROP TABLE moderator_flag_checks;
