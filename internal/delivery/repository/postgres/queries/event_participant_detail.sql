-- name: GetEventParticipantDetail :one
-- One participant for the manager's detail view: the same joined columns as
-- the moderation list, the role in the team, and their own attempt figures
-- (attempts submitted, distinct tasks solved). A participant of the moderators
-- team is a member like any other here.
SELECT participant.*,
       person.first_name,
       person.last_name,
       person.email,
       event_participant_public_name(participant.event_id, participant.user_id)::text AS display_name,
       COALESCE(team.hidden, false)::boolean                                            AS team_hidden,
       COALESCE(event_team_public_name(team.individual, team.event_id, team.captain_id, team.name), '')::text AS team_name,
       COALESCE(invited_team.name, '')::text                                            AS invited_team_name,
       presence.last_seen_at                                                            AS last_seen_at,
       COALESCE(lab.at, 'epoch'::timestamptz)::timestamptz AS last_lab_at,
       (SELECT count(*) FROM effective_challenge_attempts a
        WHERE a.event_id = participant.event_id AND a.user_id = participant.user_id)::bigint AS attempts,
       (SELECT count(DISTINCT a.team_challenge_id) FROM effective_challenge_attempts a
        WHERE a.event_id = participant.event_id AND a.user_id = participant.user_id AND a.effective_correct)::bigint AS solves
FROM event_participants participant
         JOIN users person ON person.id = participant.user_id
         LEFT JOIN event_teams team ON team.id = participant.team_id
         LEFT JOIN event_teams invited_team ON invited_team.id = participant.invited_team_id
         LEFT JOIN event_participant_presence presence ON presence.event_id = participant.event_id AND presence.user_id = participant.user_id
         CROSS JOIN LATERAL (SELECT event_user_last_lab_at(participant.event_id, participant.user_id) AS at) lab
WHERE participant.event_id = sqlc.arg(event_id)::uuid
  AND participant.user_id = sqlc.arg(user_id)::uuid;
