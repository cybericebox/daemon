-- W4 exercises: catalog vs event ownership, catalog access levels, forks,
-- proposals to the catalog, detachable event attachments and task hints.

-- scope: 0 platform catalog, 1 owned by one event. An event-scoped row whose
-- owner event was deleted keeps scope 1 with a NULL owner (an archived orphan),
-- so it never turns into a catalog entry.
-- access_level (catalog only): 0 every event, 1 selected events, 2 only the
-- origin event (the one it was proposed from).
ALTER TABLE exercises
    ADD COLUMN scope                   smallint NOT NULL DEFAULT 0 CHECK (scope IN (0, 1)),
    ADD COLUMN owner_event_id          uuid REFERENCES events (id) ON DELETE SET NULL,
    ADD COLUMN access_level            smallint NOT NULL DEFAULT 0 CHECK (access_level IN (0, 1, 2)),
    ADD COLUMN origin_event_id         uuid REFERENCES events (id) ON DELETE SET NULL,
    ADD COLUMN forked_from_exercise_id uuid REFERENCES exercises (id) ON DELETE SET NULL,
    ADD COLUMN forked_from_version_id  uuid REFERENCES exercise_versions (id) ON DELETE SET NULL,
    ADD CONSTRAINT exercises_scope_owner_check CHECK (scope = 1 OR owner_event_id IS NULL);

-- Names are unique per scope: among catalog entries and within one event.
ALTER TABLE exercises
    DROP CONSTRAINT exercises_name_key;
CREATE UNIQUE INDEX exercises_catalog_name_uq ON exercises (name) WHERE scope = 0;
CREATE UNIQUE INDEX exercises_event_name_uq ON exercises (owner_event_id, name) WHERE scope = 1;
CREATE INDEX exercises_owner_event_idx ON exercises (owner_event_id) WHERE owner_event_id IS NOT NULL;
CREATE INDEX exercises_forked_from_idx ON exercises (forked_from_exercise_id) WHERE forked_from_exercise_id IS NOT NULL;

-- Events a catalog exercise with access_level = 1 is available to.
CREATE TABLE exercise_event_access
(
    exercise_id uuid NOT NULL REFERENCES exercises (id) ON DELETE CASCADE,
    event_id    uuid NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    PRIMARY KEY (exercise_id, event_id)
);
CREATE INDEX exercise_event_access_event_idx ON exercise_event_access (event_id);

-- A manager proposes an event exercise to the catalog; an admin approves
-- (copying it into a new catalog exercise) or rejects. status: 0 pending,
-- 1 approved, 2 rejected.
CREATE TABLE exercise_proposals
(
    id                  uuid PRIMARY KEY,
    exercise_id         uuid        NOT NULL REFERENCES exercises (id) ON DELETE CASCADE,
    event_id            uuid        REFERENCES events (id) ON DELETE SET NULL,
    status              smallint    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    note                text        NOT NULL DEFAULT '',
    proposed_by         uuid        REFERENCES users (id) ON DELETE SET NULL,
    proposed_at         timestamptz NOT NULL,
    decided_by          uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at          timestamptz,
    decision_note       text        NOT NULL DEFAULT '',
    catalog_exercise_id uuid        REFERENCES exercises (id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX exercise_proposals_pending_uq ON exercise_proposals (exercise_id) WHERE status = 0;
CREATE INDEX exercise_proposals_status_idx ON exercise_proposals (status, proposed_at DESC);

-- Visibility rules in one place (lists, reads, the event catalog).
-- An exercise is available to an event when the event owns it, or it is a
-- catalog exercise whose access level admits the event.
CREATE FUNCTION exercise_available_to_event(p_exercise_id uuid, p_event_id uuid)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT EXISTS (SELECT 1
               FROM exercises e
               WHERE e.id = p_exercise_id
                 AND ((e.scope = 1 AND e.owner_event_id = p_event_id)
                   OR (e.scope = 0 AND (e.access_level = 0
                       OR (e.access_level = 1 AND EXISTS (SELECT 1
                                                          FROM exercise_event_access access
                                                          WHERE access.exercise_id = e.id
                                                            AND access.event_id = p_event_id))
                       OR (e.access_level = 2 AND e.origin_event_id = p_event_id)))))
$$;

-- A non-admin reads the exercises of events they are a member of, and the
-- published, active catalog exercises available to any of those events.
CREATE FUNCTION exercise_readable_by(p_exercise_id uuid, p_viewer_id uuid)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT EXISTS (SELECT 1
               FROM exercises e
               JOIN event_managers member ON member.user_id = p_viewer_id
               WHERE e.id = p_exercise_id
                 AND ((e.scope = 1 AND e.owner_event_id = member.event_id)
                   OR (e.scope = 0 AND e.published_version_id IS NOT NULL AND e.archived_at IS NULL
                       AND exercise_available_to_event(e.id, member.event_id))))
$$;

-- An exercise has infrastructure when its published (else working) content
-- has a lab topology with devices.
CREATE FUNCTION exercise_has_infrastructure(p_exercise_id uuid)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT COALESCE((SELECT jsonb_path_exists(version.variants, '$[*].topology.devices[*]')
                 FROM exercises e
                 JOIN exercise_versions version ON version.id = COALESCE(e.published_version_id, e.draft_version_id)
                 WHERE e.id = p_exercise_id), false)
$$;

-- status 2 = detached: kept (it has attempts) but off every board and score.
ALTER TABLE event_exercises
    DROP CONSTRAINT event_exercises_status_check,
    ADD CONSTRAINT event_exercises_status_check CHECK (status IN (0, 1, 2)),
    ADD COLUMN detached_at timestamptz,
    ADD COLUMN detached_by uuid REFERENCES users (id) ON DELETE SET NULL;

-- Hints: the canonical hint list (id, cost, text) of the board challenge, the
-- event's cost overrides {hintID: cost}, and the team's variant texts (never
-- part of the participant snapshot).
ALTER TABLE event_challenges
    ADD COLUMN hints      jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN hint_costs jsonb NOT NULL DEFAULT '{}';

ALTER TABLE team_challenges
    ADD COLUMN hints jsonb NOT NULL DEFAULT '[]';

CREATE TABLE team_challenge_hint_unlocks
(
    team_challenge_id  uuid        NOT NULL REFERENCES team_challenges (id) ON DELETE CASCADE,
    hint_id            uuid        NOT NULL,
    event_id           uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    event_team_id      uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    event_challenge_id uuid        NOT NULL REFERENCES event_challenges (id) ON DELETE CASCADE,
    unlocked_by        uuid        REFERENCES users (id) ON DELETE SET NULL,
    unlocked_at        timestamptz NOT NULL,
    cost               integer     NOT NULL CHECK (cost >= 0),
    PRIMARY KEY (team_challenge_id, hint_id)
);
CREATE INDEX team_challenge_hint_unlocks_event_idx ON team_challenge_hint_unlocks (event_id, unlocked_at);

-- 0 reward (A): hint costs reduce the reward of the solve, never below 0.
-- 1 balance (B): a paid unlock is deducted from the score at unlock time.
ALTER TABLE event_configs
    ADD COLUMN hint_charge_mode smallint NOT NULL DEFAULT 0 CHECK (hint_charge_mode IN (0, 1));

-- The single scoring formula learns hint costs and detached attachments. The
-- row type gains `solve`: false marks a balance-mode hint penalty row (its
-- points are negative and it is not a solve).
-- The view is dropped and recreated through EXECUTE (as in 0082) so sqlc keeps
-- its original catalog shape; queries needing `solve` read the function.
DO
$$
    BEGIN
        EXECUTE 'DROP VIEW event_solved_scores';
    END
$$;
DROP FUNCTION event_solved_scores_at(uuid, timestamptz, uuid);
DROP TYPE event_solved_score;

CREATE TYPE event_solved_score AS
(
    event_id           uuid,
    event_team_id      uuid,
    team_challenge_id  uuid,
    event_challenge_id uuid,
    solved_at          timestamptz,
    points             integer,
    solve              boolean
);

CREATE FUNCTION event_solved_scores_at(p_event_id uuid, p_cutoff timestamptz, p_include_team uuid)
    RETURNS SETOF event_solved_score
    LANGUAGE sql
    STABLE
AS
$$
WITH charge AS (
    SELECT COALESCE((SELECT config.hint_charge_mode FROM event_configs config WHERE config.event_id = p_event_id), 0) AS mode
), solves AS (
    SELECT tc.event_id, tc.event_team_id, tc.id AS team_challenge_id,
           tc.event_challenge_id, solved.solved_at, solved.awarded_points,
           team.hidden, ec.points AS static_points,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.scoring_mode ELSE ec.scoring_mode END AS scoring_mode,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_min_points ELSE ec.dynamic_min_points END AS min_points,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_max_points ELSE ec.dynamic_max_points END AS max_points,
           CASE WHEN e.force_event_scoring OR ec.scoring_mode IS NULL THEN e.dynamic_floor_at_percent ELSE ec.dynamic_floor_at_percent END AS floor_percent,
           greatest(1, least(COALESCE(population.units_count, 1),
               (SELECT count(*) FROM event_teams population_team
                WHERE population_team.event_id = e.id AND NOT population_team.hidden))) AS population,
           count(*) FILTER (WHERE NOT team.hidden) OVER (PARTITION BY tc.event_challenge_id) AS visible_solve_count,
           count(*) FILTER (WHERE NOT team.hidden) OVER (
               PARTITION BY tc.event_challenge_id
               ORDER BY solved.solved_at, tc.id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
           ) AS visible_rank,
           CASE WHEN (SELECT mode FROM charge) = 0 THEN
               (SELECT COALESCE(sum(unlock.cost), 0)
                FROM team_challenge_hint_unlocks unlock
                WHERE unlock.team_challenge_id = tc.id
                  AND unlock.unlocked_at <= solved.solved_at)
           ELSE 0 END AS hint_cost
    FROM team_challenge_solves solved
    JOIN team_challenges tc ON tc.id = solved.team_challenge_id
    JOIN event_teams team ON team.id = tc.event_team_id
    JOIN event_challenges ec ON ec.id = tc.event_challenge_id
    JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.status <> 2
    JOIN events e ON e.id = tc.event_id
    LEFT JOIN event_scoring_populations population ON population.event_id = e.id
    WHERE tc.event_id = p_event_id
      AND (p_cutoff IS NULL OR solved.solved_at < p_cutoff OR tc.event_team_id = p_include_team)
), progress AS (
    SELECT solves.*,
           least(1::numeric, visible_solve_count::numeric /
                 greatest(1::numeric, population::numeric * floor_percent::numeric / 100)) AS popularity_progress,
           ceil(population::numeric * floor_percent::numeric / 100) AS ladder_floor_units
    FROM solves
), ranked AS (
    SELECT progress.*,
           least(1::numeric, greatest(0::numeric,
                 (visible_rank - 1)::numeric / greatest(1::numeric, ladder_floor_units - 1))) AS ladder_progress
    FROM progress
)
SELECT event_id, event_team_id, team_challenge_id, event_challenge_id, solved_at,
       greatest(0, (CASE WHEN hidden THEN static_points
            WHEN scoring_mode = 1 THEN round(max_points - (max_points - min_points) *
                 power(popularity_progress, 2) * (3 - 2 * popularity_progress))::integer
            WHEN scoring_mode = 2 THEN
                CASE WHEN ladder_floor_units <= 1 THEN min_points
                     ELSE round(max_points - (max_points - min_points) *
                          power(ladder_progress, 2) * (3 - 2 * ladder_progress))::integer END
            ELSE COALESCE(awarded_points, static_points) END) - hint_cost)::integer AS points,
       true AS solve
FROM ranked
UNION ALL
SELECT unlock.event_id, unlock.event_team_id, unlock.team_challenge_id, unlock.event_challenge_id,
       unlock.unlocked_at, (-unlock.cost)::integer, false
FROM team_challenge_hint_unlocks unlock
JOIN event_challenges ec ON ec.id = unlock.event_challenge_id
JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.status <> 2
WHERE unlock.event_id = p_event_id
  AND (SELECT mode FROM charge) = 1
  AND unlock.cost > 0
  AND (p_cutoff IS NULL OR unlock.unlocked_at < p_cutoff OR unlock.event_team_id = p_include_team)
$$;

DO
$$
    BEGIN
        EXECUTE 'CREATE VIEW event_solved_scores AS
            SELECT score.event_id, score.event_team_id, score.team_challenge_id,
                   score.event_challenge_id, score.solved_at, score.points, score.solve
            FROM events e
            CROSS JOIN LATERAL event_solved_scores_at(e.id, NULL, NULL) score';
    END
$$;
