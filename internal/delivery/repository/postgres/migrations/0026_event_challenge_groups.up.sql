CREATE TABLE event_challenge_groups
(
    id          uuid        PRIMARY KEY,
    event_id    uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    order_index integer     NOT NULL,
    created_at  timestamptz NOT NULL,
    CONSTRAINT event_challenge_groups_name_check CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    UNIQUE (event_id, name),
    UNIQUE (event_id, order_index)
);

ALTER TABLE event_challenges
    ADD COLUMN group_id uuid REFERENCES event_challenge_groups (id) ON DELETE SET NULL;

CREATE INDEX event_challenge_groups_event_order_idx
    ON event_challenge_groups (event_id, order_index);
CREATE INDEX event_challenges_group_idx ON event_challenges (group_id);
