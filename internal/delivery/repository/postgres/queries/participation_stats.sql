-- name: ListParticipationSolves :many
-- The solves of one team for its participation page, live: the task, its
-- category (the task group), the points, who solved it (the earliest effective
-- correct attempt) and whether it was the first solve among ranked teams.
-- Answers are never read here.
SELECT ranked.event_challenge_id::uuid AS event_challenge_id,
       COALESCE(challenge.snapshot->>'name', '')::text AS challenge_name,
       COALESCE(grp.name, 'Інші')::text AS category,
       ranked.points::integer AS points,
       ranked.solved_at::timestamptz AS solved_at,
       COALESCE(solver.user_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS solved_by,
       COALESCE(event_participant_public_name(sqlc.arg(event_id)::uuid, solver.user_id),
                NULLIF(btrim(concat_ws(' ', solver.first_name, solver.last_name)), ''), '')::text AS solved_by_name,
       (ranked.counted AND ranked.place = 1)::boolean AS first_blood
FROM (
    SELECT s.event_team_id::uuid AS event_team_id, s.event_challenge_id::uuid AS event_challenge_id, s.team_challenge_id::uuid AS team_challenge_id, s.points::integer AS points, s.solved_at::timestamptz AS solved_at,
           (event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually, team.admission_locked, team.member_count))::boolean AS counted,
           row_number() OVER (
               PARTITION BY s.event_challenge_id,
                   (event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually, team.admission_locked, team.member_count))
               ORDER BY s.solved_at, s.team_challenge_id) AS place
    FROM event_solved_scores_at(sqlc.arg(event_id)::uuid, NULL, NULL) s
    JOIN event_teams team ON team.id = s.event_team_id
    WHERE s.solve
) ranked
LEFT JOIN event_challenges challenge ON challenge.id = ranked.event_challenge_id
LEFT JOIN event_challenge_groups grp ON grp.id = challenge.group_id
LEFT JOIN LATERAL (
    SELECT a.user_id, person.first_name, person.last_name
    FROM effective_challenge_attempts a
    JOIN users person ON person.id = a.user_id
    WHERE a.team_challenge_id = ranked.team_challenge_id AND a.effective_correct
    ORDER BY a.received_at ASC, a.id ASC
    LIMIT 1
) solver ON TRUE
WHERE ranked.event_team_id = sqlc.arg(event_team_id)::uuid
ORDER BY ranked.solved_at ASC, ranked.team_challenge_id ASC;

-- name: ListParticipationMembers :many
-- The roster of one team with each member's own counters: attempts (only the
-- counts, never the answers), correct attempts and opened hints.
SELECT participant.user_id,
       participant.team_role,
       event_participant_public_name(participant.event_id, participant.user_id)::text AS display_name,
       COALESCE(participant.decided_at, participant.created_at)::timestamptz AS joined_at,
       (SELECT count(*) FROM effective_challenge_attempts a
         WHERE a.event_id = participant.event_id AND a.event_team_id = sqlc.arg(event_team_id)::uuid AND a.user_id = participant.user_id)::bigint AS attempts,
       (SELECT count(*) FROM effective_challenge_attempts a
         WHERE a.event_id = participant.event_id AND a.event_team_id = sqlc.arg(event_team_id)::uuid AND a.user_id = participant.user_id AND a.effective_correct)::bigint AS correct_attempts,
       (SELECT count(*) FROM team_challenge_hint_unlocks unlock
         WHERE unlock.event_team_id = sqlc.arg(event_team_id)::uuid AND unlock.unlocked_by = participant.user_id)::bigint AS hints
FROM event_participants participant
WHERE participant.event_id = sqlc.arg(event_id)
  AND participant.team_id = sqlc.arg(event_team_id)::uuid
ORDER BY participant.team_role ASC, display_name ASC, participant.user_id ASC;

-- name: ListModeratorsParticipationMembers :many
-- The roster of the moderators team: the event managers (owner first), each
-- with the counters of what they did as the team. Same shape as
-- ListParticipationMembers; team_role 0 is the owner, 1 everyone else.
SELECT manager.user_id,
       (CASE WHEN manager.role = 0 THEN 0 ELSE 1 END)::smallint AS team_role,
       COALESCE(NULLIF(btrim(concat_ws(' ', person.first_name, person.last_name)), ''), person.email)::text AS display_name,
       manager.created_at::timestamptz AS joined_at,
       (SELECT count(*) FROM effective_challenge_attempts a
         WHERE a.event_id = manager.event_id AND a.event_team_id = sqlc.arg(event_team_id)::uuid AND a.user_id = manager.user_id)::bigint AS attempts,
       (SELECT count(*) FROM effective_challenge_attempts a
         WHERE a.event_id = manager.event_id AND a.event_team_id = sqlc.arg(event_team_id)::uuid AND a.user_id = manager.user_id AND a.effective_correct)::bigint AS correct_attempts,
       (SELECT count(*) FROM team_challenge_hint_unlocks unlock
         WHERE unlock.event_team_id = sqlc.arg(event_team_id)::uuid AND unlock.unlocked_by = manager.user_id)::bigint AS hints
FROM event_managers manager
JOIN users person ON person.id = manager.user_id
WHERE manager.event_id = sqlc.arg(event_id)
ORDER BY team_role ASC, display_name ASC, manager.user_id ASC;

-- name: GetParticipationTeamTotals :one
-- Team-wide counters, including members who have left since.
SELECT (SELECT count(*) FROM effective_challenge_attempts a
         WHERE a.event_id = sqlc.arg(event_id) AND a.event_team_id = sqlc.arg(event_team_id)::uuid)::bigint AS attempts,
       (SELECT count(*) FROM effective_challenge_attempts a
         WHERE a.event_id = sqlc.arg(event_id) AND a.event_team_id = sqlc.arg(event_team_id)::uuid AND a.effective_correct)::bigint AS correct_attempts,
       (SELECT count(*) FROM team_challenge_hint_unlocks unlock
         WHERE unlock.event_id = sqlc.arg(event_id) AND unlock.event_team_id = sqlc.arg(event_team_id)::uuid)::bigint AS hints;
