CREATE TABLE event_challenge_prerequisites
(
    challenge_id              uuid NOT NULL REFERENCES event_challenges (id) ON DELETE CASCADE,
    prerequisite_challenge_id uuid NOT NULL REFERENCES event_challenges (id) ON DELETE CASCADE,
    PRIMARY KEY (challenge_id, prerequisite_challenge_id),
    CONSTRAINT event_challenge_prerequisites_distinct_check CHECK (challenge_id <> prerequisite_challenge_id)
);

CREATE INDEX event_challenge_prerequisites_prerequisite_idx
    ON event_challenge_prerequisites (prerequisite_challenge_id);
