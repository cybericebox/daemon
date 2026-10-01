ALTER TABLE event_configs
    ADD COLUMN brand_color varchar(7) NOT NULL DEFAULT '#211A52',
    ADD COLUMN accent_color varchar(7) NOT NULL DEFAULT '',
    ADD COLUMN accent_light varchar(7) NOT NULL DEFAULT '#211A52',
    ADD COLUMN accent_dark varchar(7) NOT NULL DEFAULT '#E6E6EE',
    ADD COLUMN accent_live varchar(7) NOT NULL DEFAULT '#FFFFFF',
    ADD COLUMN theme_version bigint NOT NULL DEFAULT 1;
