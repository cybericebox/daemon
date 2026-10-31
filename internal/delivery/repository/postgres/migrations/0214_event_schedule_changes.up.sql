-- Who moved an event's schedule, when, from what to what. A trigger on events records every change of the schedule
-- columns, whichever code path (or hand edit) made it: khctf 2026-10-26 needed the request audit and a guess to learn
-- who moved the start from 11:25 to 11:45.
CREATE TABLE event_schedule_changes
(
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id               uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    changed_at             timestamptz NOT NULL DEFAULT now(),
    -- The account the application stored in events.updated_by; for an edit that did not go through the
    -- application (events.updated_at did not move) it is the previous value and by_application is false.
    changed_by             uuid REFERENCES users (id) ON DELETE SET NULL,
    by_application         boolean     NOT NULL,
    db_role                text        NOT NULL DEFAULT current_user,
    old_join_policy        smallint    NOT NULL,
    new_join_policy        smallint    NOT NULL,
    old_publish_at         timestamptz NOT NULL,
    new_publish_at         timestamptz NOT NULL,
    old_start_at           timestamptz NOT NULL,
    new_start_at           timestamptz NOT NULL,
    old_finish_at          timestamptz,
    new_finish_at          timestamptz,
    old_withdraw_at        timestamptz,
    new_withdraw_at        timestamptz,
    old_manual_finished_at timestamptz,
    new_manual_finished_at timestamptz
);

CREATE INDEX event_schedule_changes_event_idx ON event_schedule_changes (event_id, changed_at DESC);

CREATE FUNCTION record_event_schedule_change() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    INSERT INTO event_schedule_changes (event_id, changed_by, by_application,
                                        old_join_policy, new_join_policy, old_publish_at, new_publish_at,
                                        old_start_at, new_start_at, old_finish_at, new_finish_at,
                                        old_withdraw_at, new_withdraw_at, old_manual_finished_at, new_manual_finished_at)
    VALUES (NEW.id, NEW.updated_by, NEW.updated_at IS DISTINCT FROM OLD.updated_at,
            OLD.join_policy, NEW.join_policy, OLD.publish_at, NEW.publish_at,
            OLD.start_at, NEW.start_at, OLD.finish_at, NEW.finish_at,
            OLD.withdraw_at, NEW.withdraw_at, OLD.manual_finished_at, NEW.manual_finished_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER events_schedule_change
    AFTER UPDATE
    ON events
    FOR EACH ROW
    WHEN (OLD.join_policy IS DISTINCT FROM NEW.join_policy
        OR OLD.publish_at IS DISTINCT FROM NEW.publish_at
        OR OLD.start_at IS DISTINCT FROM NEW.start_at
        OR OLD.finish_at IS DISTINCT FROM NEW.finish_at
        OR OLD.withdraw_at IS DISTINCT FROM NEW.withdraw_at
        OR OLD.manual_finished_at IS DISTINCT FROM NEW.manual_finished_at)
EXECUTE FUNCTION record_event_schedule_change();
