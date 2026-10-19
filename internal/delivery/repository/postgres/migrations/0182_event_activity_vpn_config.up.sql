-- Audit of lab VPN config downloads: who, event, config type (data.config).
ALTER TABLE event_activity
    DROP CONSTRAINT event_activity_kind_check,
    ADD CONSTRAINT event_activity_kind_check CHECK (kind IN ('task_opened', 'attachment_downloaded', 'hint_viewed',
                                                             'team_joined', 'team_left', 'captain_changed',
                                                             'participant_status_changed', 'attempt_rejected',
                                                             'vpn_config_downloaded'));
