ALTER TABLE event_challenges
    DROP CONSTRAINT IF EXISTS event_challenges_max_flag_attempts_check,
    DROP COLUMN IF EXISTS max_flag_attempts;
ALTER TABLE event_configs
    DROP CONSTRAINT IF EXISTS event_configs_max_flag_attempts_check,
    DROP COLUMN IF EXISTS max_flag_attempts;
