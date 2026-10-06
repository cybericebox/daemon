-- W2 participation model: pseudonyms, invitation delivery state, hidden
-- individual teams, team admission and the shared extra-field column config.

ALTER TABLE event_configs
    ADD COLUMN allow_pseudonyms boolean NOT NULL DEFAULT false;

ALTER TABLE event_participants
    ADD COLUMN pseudonym text,
    ADD COLUMN invitation_sent_at timestamptz,
    ADD CONSTRAINT event_participants_pseudonym_check CHECK (pseudonym IS NULL OR char_length(pseudonym) BETWEEN 2 AND 32);

CREATE UNIQUE INDEX event_participants_pseudonym_uq
    ON event_participants (event_id, lower(pseudonym))
    WHERE pseudonym IS NOT NULL;

-- Legacy invitations were only persisted after their email had been queued
-- successfully or failed without a trace; treat them as delivered.
UPDATE event_participants SET invitation_sent_at = created_at WHERE invited;

ALTER TABLE event_teams
    ADD COLUMN individual boolean NOT NULL DEFAULT false,
    ADD COLUMN admitted_manually boolean NOT NULL DEFAULT false,
    ADD COLUMN admission_locked boolean NOT NULL DEFAULT false;

UPDATE event_teams team
SET individual = true
FROM event_configs config
WHERE config.event_id = team.event_id
  AND config.participation = 0;

-- Teams of already started events keep their access: admission is decided
-- at start and the minimum did not exist when they entered.
UPDATE event_teams team
SET admission_locked = true
FROM events event
WHERE event.id = team.event_id
  AND event.lifecycle_configured
  AND event.start_at <= now();

CREATE TABLE event_list_columns
(
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    list       text        NOT NULL CHECK (list IN ('participants', 'teams')),
    columns    jsonb       NOT NULL CHECK (jsonb_typeof(columns) = 'array'),
    updated_at timestamptz NOT NULL,
    updated_by uuid REFERENCES users (id) ON DELETE SET NULL,
    PRIMARY KEY (event_id, list)
);

-- Public name of a participant: the pseudonym when the event allows them,
-- otherwise the profile name. Resolved at read so profile edits apply at once.
CREATE FUNCTION event_participant_public_name(p_event_id uuid, p_user_id uuid)
    RETURNS text
    LANGUAGE sql
    STABLE
AS
$$
SELECT COALESCE(
               CASE WHEN config.allow_pseudonyms THEN NULLIF(btrim(participant.pseudonym), '') END,
               NULLIF(btrim(concat_ws(' ', person.first_name, person.last_name)), ''),
               'Учасник')
FROM users person
         LEFT JOIN event_participants participant
                   ON participant.event_id = p_event_id AND participant.user_id = person.id
         LEFT JOIN event_configs config ON config.event_id = p_event_id
WHERE person.id = p_user_id
$$;

-- Individual events keep a hidden single-member team; it is presented by its
-- participant's public name, never by its stored technical name.
CREATE FUNCTION event_team_public_name(p_individual boolean, p_event_id uuid, p_captain_id uuid, p_name text)
    RETURNS text
    LANGUAGE sql
    STABLE
AS
$$
SELECT CASE
           WHEN p_individual THEN COALESCE(event_participant_public_name(p_event_id, p_captain_id), p_name)
           ELSE p_name END
$$;

-- Effective minimum: configured value, else 2 (capped by the maximum) in team
-- mode, else 1. Mirrors EventConfig.EffectiveMinTeamSize.
CREATE FUNCTION event_min_team_size(p_event_id uuid)
    RETURNS integer
    LANGUAGE sql
    STABLE
AS
$$
SELECT COALESCE(config.min_team_size,
                CASE WHEN config.participation = 1 THEN LEAST(2, config.max_team_size) ELSE 1 END)
FROM event_configs config
WHERE config.event_id = p_event_id
$$;

CREATE FUNCTION event_team_admitted(p_event_id uuid, p_individual boolean, p_admitted_manually boolean,
                                    p_admission_locked boolean, p_member_count integer)
    RETURNS boolean
    LANGUAGE sql
    STABLE
AS
$$
SELECT p_individual OR p_admitted_manually OR p_admission_locked
           OR p_member_count >= COALESCE(event_min_team_size(p_event_id), 1)
$$;
