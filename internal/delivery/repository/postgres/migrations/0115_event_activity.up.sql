-- Event analytics raw data (docs/EVENT-ANALYTICS.md §3, D1/D2/D4). Both logs
-- are append-only and kept until the event's end plus 365 days (Privacy
-- Policy, "Retention"); a deleted account's rows lose their user reference.
--
-- user_id and team_id carry no foreign key on purpose: a log row must never
-- block (or be blocked by) the deletion of the account or team it mentions.

-- event_activity: what participants did on the event site and how their
-- participation changed. kind is one of the eventActivity model kinds;
-- subject_id is the event challenge for task-level kinds; data holds small
-- kind-specific details (a rejection reason, a status transition).
CREATE TABLE event_activity
(
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    user_id    uuid,
    team_id    uuid,
    kind       text        NOT NULL CHECK (kind IN ('task_opened', 'attachment_downloaded', 'hint_viewed',
                                                    'team_joined', 'team_left', 'captain_changed',
                                                    'participant_status_changed', 'attempt_rejected')),
    subject_id uuid,
    at         timestamptz NOT NULL,
    data       jsonb       NOT NULL DEFAULT '{}' CHECK (octet_length(data::text) <= 2048)
);

CREATE INDEX event_activity_kind_idx ON event_activity (event_id, kind, at);
CREATE INDEX event_activity_subject_idx ON event_activity (event_id, subject_id) WHERE subject_id IS NOT NULL;
CREATE INDEX event_activity_user_idx ON event_activity (user_id) WHERE user_id IS NOT NULL;

-- event_stand_transitions: every status change of a team stand
-- (event_team_stands.status) and of a task laboratory (lab_bindings.readiness),
-- with the generation it belongs to. Statuses are the smallint codes of the
-- eventStand / labBinding models.
CREATE TABLE event_stand_transitions
(
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id     uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id      uuid        NOT NULL,
    source       text        NOT NULL CHECK (source IN ('stand', 'lab')),
    challenge_id uuid,
    generation   integer     NOT NULL,
    from_status  smallint,
    to_status    smallint    NOT NULL,
    reason       text,
    at           timestamptz NOT NULL
);

CREATE INDEX event_stand_transitions_event_idx ON event_stand_transitions (event_id, team_id, at);

-- The participation and stand logs are written by triggers, so every write
-- path (use cases, batch statements, retention) is recorded. A log failure is
-- only a warning: it never fails the change it describes. Rows are skipped
-- when the event itself is being deleted (a cascade), so the log cannot
-- block that deletion either.

CREATE FUNCTION event_activity_log_participant() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
BEGIN
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM events WHERE id = NEW.event_id) THEN
            RETURN NULL;
        END IF;
        IF TG_OP = 'INSERT' THEN
            INSERT INTO event_activity (event_id, user_id, team_id, kind, at, data)
            VALUES (NEW.event_id, NEW.user_id, NEW.team_id, 'participant_status_changed', now(),
                    jsonb_build_object('to', NEW.status, 'invited', NEW.invited));
            IF NEW.team_id IS NOT NULL THEN
                INSERT INTO event_activity (event_id, user_id, team_id, kind, at)
                VALUES (NEW.event_id, NEW.user_id, NEW.team_id, 'team_joined', now());
            END IF;
            RETURN NULL;
        END IF;
        IF NEW.status IS DISTINCT FROM OLD.status THEN
            INSERT INTO event_activity (event_id, user_id, team_id, kind, at, data)
            VALUES (NEW.event_id, NEW.user_id, NEW.team_id, 'participant_status_changed', now(),
                    jsonb_strip_nulls(jsonb_build_object('from', OLD.status, 'to', NEW.status, 'by', NEW.decided_by)));
        END IF;
        IF NEW.team_id IS DISTINCT FROM OLD.team_id THEN
            IF OLD.team_id IS NOT NULL THEN
                INSERT INTO event_activity (event_id, user_id, team_id, kind, at)
                VALUES (NEW.event_id, NEW.user_id, OLD.team_id, 'team_left', now());
            END IF;
            IF NEW.team_id IS NOT NULL THEN
                INSERT INTO event_activity (event_id, user_id, team_id, kind, at)
                VALUES (NEW.event_id, NEW.user_id, NEW.team_id, 'team_joined', now());
            END IF;
        END IF;
    EXCEPTION
        WHEN OTHERS THEN RAISE WARNING 'event_activity: participant log skipped: %', SQLERRM;
    END;
    RETURN NULL;
END
$$;

CREATE TRIGGER event_participants_activity
    AFTER INSERT OR UPDATE OF status, team_id
    ON event_participants
    FOR EACH ROW
EXECUTE FUNCTION event_activity_log_participant();

CREATE FUNCTION event_activity_log_captain() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
BEGIN
    BEGIN
        IF NEW.captain_id IS DISTINCT FROM OLD.captain_id
            AND EXISTS (SELECT 1 FROM events WHERE id = NEW.event_id) THEN
            INSERT INTO event_activity (event_id, user_id, team_id, kind, at, data)
            VALUES (NEW.event_id, NEW.captain_id, NEW.id, 'captain_changed', now(),
                    jsonb_build_object('from', OLD.captain_id));
        END IF;
    EXCEPTION
        WHEN OTHERS THEN RAISE WARNING 'event_activity: captain log skipped: %', SQLERRM;
    END;
    RETURN NULL;
END
$$;

CREATE TRIGGER event_teams_captain_activity
    AFTER UPDATE OF captain_id
    ON event_teams
    FOR EACH ROW
EXECUTE FUNCTION event_activity_log_captain();

CREATE FUNCTION event_stand_log_transition() RETURNS trigger
    LANGUAGE plpgsql
AS
$$
BEGIN
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM events WHERE id = NEW.event_id) THEN
            RETURN NULL;
        END IF;
        IF TG_TABLE_NAME = 'event_team_stands' THEN
            IF TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status OR NEW.generation IS DISTINCT FROM OLD.generation THEN
                INSERT INTO event_stand_transitions (event_id, team_id, source, generation, from_status, to_status, reason, at)
                VALUES (NEW.event_id, NEW.event_team_id, 'stand', NEW.generation,
                        CASE WHEN TG_OP = 'UPDATE' THEN OLD.status END, NEW.status, NEW.reason, NEW.status_changed_at);
            END IF;
        ELSE
            IF TG_OP = 'INSERT' OR NEW.readiness IS DISTINCT FROM OLD.readiness OR NEW.generation IS DISTINCT FROM OLD.generation THEN
                INSERT INTO event_stand_transitions (event_id, team_id, source, challenge_id, generation, from_status, to_status, reason, at)
                VALUES (NEW.event_id, NEW.event_team_id, 'lab', NEW.event_challenge_id, NEW.generation,
                        CASE WHEN TG_OP = 'UPDATE' THEN OLD.readiness END, NEW.readiness, NEW.failure_reason, now());
            END IF;
        END IF;
    EXCEPTION
        WHEN OTHERS THEN RAISE WARNING 'event_stand_transitions: log skipped: %', SQLERRM;
    END;
    RETURN NULL;
END
$$;

CREATE TRIGGER event_team_stands_transition
    AFTER INSERT OR UPDATE OF status, generation
    ON event_team_stands
    FOR EACH ROW
EXECUTE FUNCTION event_stand_log_transition();

CREATE TRIGGER lab_bindings_transition
    AFTER INSERT OR UPDATE OF readiness, generation
    ON lab_bindings
    FOR EACH ROW
EXECUTE FUNCTION event_stand_log_transition();
