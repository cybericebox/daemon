-- A task author asks a platform admin to let devices of a task version pass the platform frame (up to the
-- platform ceiling). status: 0 pending, 1 approved, 2 rejected. requested/approved are jsonb arrays of
-- {device_id, name, cpu_millicores, memory_bytes}; the approval is the values the admin approved per device and
-- later versions of the exercise keep it while every value stays at or below it.
CREATE TABLE exercise_resource_elevations
(
    id            uuid PRIMARY KEY,
    exercise_id   uuid        NOT NULL REFERENCES exercises (id) ON DELETE CASCADE,
    version_id    uuid        REFERENCES exercise_versions (id) ON DELETE SET NULL,
    status        smallint    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    reason        text        NOT NULL,
    requested     jsonb       NOT NULL,
    approved      jsonb,
    decision_note text        NOT NULL DEFAULT '',
    requested_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    requested_at  timestamptz NOT NULL,
    decided_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at    timestamptz
);
CREATE UNIQUE INDEX exercise_resource_elevations_pending_uq ON exercise_resource_elevations (exercise_id) WHERE status = 0;
CREATE INDEX exercise_resource_elevations_status_idx ON exercise_resource_elevations (status, requested_at DESC);
CREATE INDEX exercise_resource_elevations_approved_idx ON exercise_resource_elevations (exercise_id) WHERE status = 1;
