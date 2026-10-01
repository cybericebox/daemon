DROP TRIGGER IF EXISTS lab_bindings_transition ON lab_bindings;
DROP TRIGGER IF EXISTS event_team_stands_transition ON event_team_stands;
DROP TRIGGER IF EXISTS event_teams_captain_activity ON event_teams;
DROP TRIGGER IF EXISTS event_participants_activity ON event_participants;
DROP FUNCTION IF EXISTS event_stand_log_transition();
DROP FUNCTION IF EXISTS event_activity_log_captain();
DROP FUNCTION IF EXISTS event_activity_log_participant();
DROP TABLE IF EXISTS event_stand_transitions;
DROP TABLE IF EXISTS event_activity;
