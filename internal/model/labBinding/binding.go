package labBindingModel

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

type Binding struct {
	ID, EventID, EventTeamID, EventChallengeID uuid.UUID
	LabID                                      uuid.NullUUID
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

// Names derives stable infrastructure handles. The Lab belongs to the event
// exercise and the variant the team was given, so every task of the set shares
// one Lab; retries retain the same handles.
func Names(eventID, teamID, eventExerciseID uuid.UUID, variantIndex int32) (group, lab string, err error) {
	if eventID == uuid.Nil || teamID == uuid.Nil || eventExerciseID == uuid.Nil || variantIndex < 0 {
		return "", "", ErrLabBindingInvalid.Err()
	}
	group, err = GroupName(eventID, teamID)
	if err != nil {
		return "", "", err
	}
	return group, LabName(eventExerciseID, variantIndex, 0), nil
}

// LabName is the agent Lab name of one generation of an exercise's variant:
// x-<event exercise>-v<variant>[-g<generation>]. Generation 0 keeps the
// original name; a recreated Lab gets a new one because deleting the previous
// Lab is asynchronous in the agent. At most 2+25+2+10+2+10 characters, inside
// the 63 of a Kubernetes name.
func LabName(eventExerciseID uuid.UUID, variantIndex, generation int32) string {
	name := "x-" + ShortID(eventExerciseID) + "-v" + strconv.Itoa(int(variantIndex))
	if generation == 0 {
		return name
	}
	return name + "-g" + strconv.Itoa(int(generation))
}

// NextLabName is the name of the next generation of a Lab, whatever its current
// generation is.
func NextLabName(current string, generation int32) string {
	base, _, _ := strings.Cut(current, "-g")
	return base + "-g" + strconv.Itoa(int(generation))
}

// IsLegacyLabName reports a per-challenge Lab name (c-<challenge>[-g<n>]) from
// before a Lab was shared by the tasks of an exercise.
func IsLegacyLabName(name string) bool { return strings.HasPrefix(name, "c-") }

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
