ALTER TABLE event_participants
    DROP COLUMN invited_team_id,
    DROP COLUMN invited_to_team;

DELETE FROM platform_signal_notification_defaults WHERE signal_type = 'participant.team_invitation.sent';
DELETE FROM notification_email_templates WHERE notification_type = 'participant.team_invitation.sent';
DELETE FROM notification_in_app_templates WHERE notification_type = 'participant.team_invitation.sent';
