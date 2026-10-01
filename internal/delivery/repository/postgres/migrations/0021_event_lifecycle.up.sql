-- Canonical lifecycle fields. The legacy availability window remains during
-- the API transition; it is backfilled into a scheduled lifecycle so existing
-- events keep a valid, deterministic state.
ALTER TABLE events
    ADD COLUMN event_type         smallint    NOT NULL DEFAULT 0,
    ADD COLUMN scored             boolean     NOT NULL DEFAULT true,
    ADD COLUMN join_policy        smallint    NOT NULL DEFAULT 0,
    ADD COLUMN schedule           smallint    NOT NULL DEFAULT 0,
    ADD COLUMN publish_at         timestamptz,
    ADD COLUMN start_at           timestamptz,
    ADD COLUMN finish_at          timestamptz,
    ADD COLUMN withdraw_at        timestamptz,
    ADD COLUMN manual_finished_at timestamptz;

-- A prior archive time becomes the automatic finish. Withdrawal follows one
-- microsecond later, preserving the old active window while creating the
-- separate Finished and Withdrawn lifecycle states required by the new model.
UPDATE events
SET publish_at = available_from,
    start_at = available_from,
    finish_at = archive_at,
    withdraw_at = archive_at + interval '1 microsecond';

ALTER TABLE events
    ALTER COLUMN publish_at SET NOT NULL,
    ALTER COLUMN start_at SET NOT NULL,
    ADD CONSTRAINT events_event_type_check CHECK (event_type IN (0, 1)),
    ADD CONSTRAINT events_join_policy_check CHECK (join_policy IN (0, 1)),
    ADD CONSTRAINT events_schedule_check CHECK (schedule IN (0, 1)),
    ADD CONSTRAINT events_lifecycle_times_check CHECK (
        start_at >= publish_at
        AND (
            (schedule = 0 AND finish_at IS NOT NULL AND withdraw_at IS NOT NULL
                AND finish_at > start_at AND withdraw_at > finish_at)
            OR (schedule = 1 AND finish_at IS NULL AND withdraw_at IS NULL)
        )
        AND (manual_finished_at IS NULL
            OR (manual_finished_at >= start_at
                AND (withdraw_at IS NULL OR manual_finished_at < withdraw_at)))
    );
