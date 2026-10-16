ALTER TABLE site_banners
    DROP CONSTRAINT site_banners_target_selection_check,
    DROP CONSTRAINT site_banners_target_sites_check,
    DROP CONSTRAINT site_banners_target_mode_check,
    DROP COLUMN target_event_ids,
    DROP COLUMN target_sites,
    DROP COLUMN target_mode;
