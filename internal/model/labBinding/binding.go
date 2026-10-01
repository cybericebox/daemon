package labBindingModel

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

type Binding struct {
	ID, EventID, EventTeamID, EventChallengeID uuid.UUID
	LabGroupName, LabName                      string
	CreatedAt                                  time.Time
	Readiness                                  Readiness
	// Generation counts moderator recreates; each has its own Lab name.
	Generation int32
	// DeployedAt is when the agent accepted the current generation's Lab.
	DeployedAt    *time.Time
	FailureReason string
}

type Readiness int16

const (
	ReadinessPending Readiness = iota
	ReadinessReady
	ReadinessFailed
	// ReadinessDestroyed is terminal: the LabGroup was removed after event
	// withdrawal, so the periodic cleanup job must never enqueue it again.
	ReadinessDestroyed
)

func New(eventID, teamID, challengeID uuid.UUID, group, lab string, now time.Time) (Binding, error) {
	group, lab = strings.TrimSpace(group), strings.TrimSpace(lab)
	if eventID == uuid.Nil || teamID == uuid.Nil || challengeID == uuid.Nil || group == "" || lab == "" {
		return Binding{}, ErrLabBindingInvalid.Err()
	}
	return Binding{ID: uuid.Must(uuid.NewV7()), EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, LabGroupName: group, LabName: lab, CreatedAt: now, Readiness: ReadinessPending}, nil
}

// Names derives stable infrastructure handles. They intentionally include the
// immutable event challenge, so each task gets one agent Lab while retries
// retain the same handles.
func Names(eventID, teamID, eventChallengeID uuid.UUID) (group, lab string, err error) {
	if eventID == uuid.Nil || teamID == uuid.Nil || eventChallengeID == uuid.Nil {
		return "", "", ErrLabBindingInvalid.Err()
	}
	group, err = GroupName(eventID, teamID)
	if err != nil {
		return "", "", err
	}
	return group, LabName(eventChallengeID, 0), nil
}

// LabName is the agent Lab name of one generation. Generation 0 keeps the
// original name; a recreated Lab gets a new one because deleting the previous
// Lab is asynchronous in the agent.
func LabName(eventChallengeID uuid.UUID, generation int32) string {
	if generation == 0 {
		return "c-" + eventChallengeID.String()
	}
	return "c-" + eventChallengeID.String() + "-g" + strconv.Itoa(int(generation))
}

// GroupName is shared by early VPN issuance and later challenge Lab deployment:
// e-<event>-t-<team> with both ids as 25-character base36 (the same encoding as a
// lab id), 56 characters, inside the 63 of a Kubernetes label and namespace.
func GroupName(eventID, teamID uuid.UUID) (string, error) {
	if eventID == uuid.Nil || teamID == uuid.Nil {
		return "", ErrLabBindingInvalid.Err()
	}
	return "e-" + ShortID(eventID) + "-t-" + ShortID(teamID), nil
}

// ParseGroupName is the inverse of GroupName. The boolean is false for any
// LabGroup that is not an event team's (for example the test-deploy groups).
func ParseGroupName(name string) (eventID, teamID uuid.UUID, ok bool) {
	rest, found := strings.CutPrefix(name, "e-")
	if !found {
		return uuid.Nil, uuid.Nil, false
	}
	eventPart, teamPart, found := strings.Cut(rest, "-t-")
	if !found {
		return uuid.Nil, uuid.Nil, false
	}
	eventID, ok = ParseShortID(eventPart)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	teamID, ok = ParseShortID(teamPart)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, teamID, true
}

// ParseLabName is the inverse of LabName for every generation.
func ParseLabName(name string) (eventChallengeID uuid.UUID, generation int32, ok bool) {
	rest, found := strings.CutPrefix(name, "c-")
	if !found {
		return uuid.Nil, 0, false
	}
	idPart, genPart, hasGeneration := strings.Cut(rest, "-g")
	eventChallengeID, err := uuid.FromString(idPart)
	if err != nil || eventChallengeID == uuid.Nil {
		return uuid.Nil, 0, false
	}
	if hasGeneration {
		n, err := strconv.ParseInt(genPart, 10, 32)
		if err != nil || n <= 0 {
			return uuid.Nil, 0, false
		}
		generation = int32(n)
	}
	return eventChallengeID, generation, true
}
