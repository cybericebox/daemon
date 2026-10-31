-- What a waiting Lab needs and what the reservation has left, kept next to its wait reason so the organizers read
-- "needs X, has Y". One row per Lab binding while it waits.
CREATE TABLE lab_binding_wait_details
(
    lab_binding_id       uuid PRIMARY KEY REFERENCES lab_bindings (id) ON DELETE CASCADE,
    need_cpu_millicores  bigint      NOT NULL,
    need_memory_bytes    bigint      NOT NULL,
    have_cpu_millicores  bigint      NOT NULL,
    have_memory_bytes    bigint      NOT NULL,
    need_storage_bytes   bigint      NOT NULL DEFAULT 0,
    have_storage_bytes   bigint      NOT NULL DEFAULT 0,
    updated_at           timestamptz NOT NULL
);
