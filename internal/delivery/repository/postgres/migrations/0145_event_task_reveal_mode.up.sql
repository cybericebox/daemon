-- How infrastructure tasks are revealed to the teams:
--   all_ready: a task is revealed when it is ready for every team (the default, an olympiad);
--   as_ready:  a task is revealed to each team as soon as its own lab is ready.
-- The organizer sets it until the event starts.
ALTER TABLE event_configs
    ADD COLUMN task_reveal_mode text NOT NULL DEFAULT 'all_ready' CHECK (task_reveal_mode IN ('all_ready', 'as_ready'));
