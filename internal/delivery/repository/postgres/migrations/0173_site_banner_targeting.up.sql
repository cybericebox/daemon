ALTER TABLE site_banners
    ADD COLUMN target_mode text NOT NULL DEFAULT 'all',
    ADD COLUMN target_sites text[] NOT NULL DEFAULT '{}',
    ADD COLUMN target_event_ids uuid[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT site_banners_target_mode_check CHECK (target_mode IN ('all', 'selected')),
    ADD CONSTRAINT site_banners_target_sites_check CHECK (target_sites <@ ARRAY['main','id','admin','exercises','event']::text[]),
    ADD CONSTRAINT site_banners_target_selection_check CHECK (
        (target_mode = 'all' AND cardinality(target_sites) = 0 AND cardinality(target_event_ids) = 0)
        OR (target_mode = 'selected' AND scope_event_id IS NULL
            AND cardinality(target_sites) + cardinality(target_event_ids) > 0)
    );
