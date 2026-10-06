ALTER TABLE events
    DROP CONSTRAINT events_live_layout_profile_check,
    DROP COLUMN live_layout_profile,
    DROP COLUMN live_layout_slots,
    ADD COLUMN live_layout jsonb NOT NULL DEFAULT '{"version":1,"theme":"dark","aspect":"16:9","screen":{"width":1920,"height":1080,"anchor":"full","textScale":1},"grid":{"cols":12,"rows":8},"widgets":[{"id":"title","type":"title","x":1,"y":1,"w":12,"h":1,"props":{}},{"id":"chart","type":"chart","x":1,"y":2,"w":8,"h":6,"props":{}},{"id":"table","type":"table","x":9,"y":2,"w":4,"h":6,"props":{}},{"id":"organizers","type":"logos","x":1,"y":8,"w":3,"h":1,"props":{"mode":"fixed","title":"Організатори"}},{"id":"partners","type":"logos","x":4,"y":8,"w":9,"h":1,"props":{"mode":"carousel","title":"Партнери"}}]}'::jsonb,
    ADD COLUMN live_layout_draft jsonb;
