package resourcesModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// ElevationStatus is where an elevation request stands.
type ElevationStatus int16

const (
	ElevationPending  ElevationStatus = 0
	ElevationApproved ElevationStatus = 1
	ElevationRejected ElevationStatus = 2
)

// String is the API name of the status.
func (s ElevationStatus) String() string {
	switch s {
	case ElevationApproved:
		return "approved"
	case ElevationRejected:
		return "rejected"
	}
	return "pending"
}

// ElevationReasonMaxRunes bounds the author's reason and the admin's note.
const ElevationReasonMaxRunes = 2000

// Elevation is an author's request, per task version, to let devices pass the platform frame (up to the
// ceiling). The approval stores the approved values per device; a later version of the exercise keeps it
// while every value stays at or below them.
type Elevation struct {
	ID           uuid.UUID
	ExerciseID   uuid.UUID
	VersionID    uuid.NullUUID
	Status       ElevationStatus
	Reason       string
	Requested    []Approval
	Approved     []Approval
	DecisionNote string
	RequestedBy  uuid.NullUUID
	RequestedAt  time.Time
	DecidedBy    uuid.NullUUID
	DecidedAt    *time.Time
}

// NewElevation is the domain factory of a pending request for the given devices.
func NewElevation(exerciseID uuid.UUID, versionID uuid.NullUUID, reason string, devices []Approval, by uuid.NullUUID, now time.Time) (Elevation, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Elevation{}, exerciseModel.ErrElevationReasonRequired.Err()
	}
	return Elevation{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: exerciseID, VersionID: versionID, Status: ElevationPending,
		Reason: truncate(reason, ElevationReasonMaxRunes), Requested: devices, RequestedBy: by, RequestedAt: now,
	}, nil
}

// Approve closes a pending request with the approved values. nil values approve exactly what was requested;
// otherwise they must be for requested devices, positive and within the ceiling (they may be lower than
// requested). Another decision on the same request is refused.
func (e *Elevation) Approve(values []Approval, ceiling Amount, by uuid.UUID, note string, now time.Time) error {
	if e.Status != ElevationPending {
		return exerciseModel.ErrElevationDecided.Err()
	}
	if values == nil {
		values = e.Requested
	}
	if !e.validApproval(values, ceiling) {
		return exerciseModel.ErrElevationInvalid.Err()
	}
	e.Status, e.Approved = ElevationApproved, values
	e.decide(by, note, now)
	return nil
}

// Reject closes a pending request.
func (e *Elevation) Reject(by uuid.UUID, note string, now time.Time) error {
	if e.Status != ElevationPending {
		return exerciseModel.ErrElevationDecided.Err()
	}
	e.Status = ElevationRejected
	e.decide(by, note, now)
	return nil
}

func (e *Elevation) decide(by uuid.UUID, note string, now time.Time) {
	e.DecisionNote = truncate(strings.TrimSpace(note), ElevationReasonMaxRunes)
	e.DecidedBy = uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
	e.DecidedAt = &now
}

// validApproval: each value is for a requested device (once), positive, and within the ceiling.
func (e *Elevation) validApproval(values []Approval, ceiling Amount) bool {
	if len(values) == 0 {
		return false
	}
	requested := map[uuid.UUID]bool{}
	for _, r := range e.Requested {
		requested[r.DeviceID] = true
	}
	seen := map[uuid.UUID]bool{}
	for _, v := range values {
		if !requested[v.DeviceID] || seen[v.DeviceID] || v.CPUMillicores <= 0 || v.MemoryBytes <= 0 || !v.Amount.Within(ceiling) {
			return false
		}
		seen[v.DeviceID] = true
	}
	return true
}

func truncate(s string, runes int) string {
	r := []rune(s)
	if len(r) <= runes {
		return s
	}
	return string(r[:runes])
}

// NeedsElevation is the devices of the variants that pass the frame, are not above the ceiling and are not
// covered by the approvals yet: what a new request has to ask for. Values are the devices' own values.
func (p Policy) NeedsElevation(variants []exerciseModel.Variant, approved []Approval) []Approval {
	var out []Approval
	seen := map[uuid.UUID]int{}
	for _, o := range p.OutsideFrame(variants) {
		if o.AboveCeiling || Covered(o, approved) {
			continue
		}
		// A device shared by variants asks for the most of them.
		if i, ok := seen[o.DeviceID]; ok {
			out[i].Amount = out[i].Amount.Max(o.Amount)
			continue
		}
		seen[o.DeviceID] = len(out)
		out = append(out, Approval{DeviceID: o.DeviceID, Name: o.Name, Amount: o.Amount})
	}
	return out
}

// ElevationListed is an elevation with the names a list shows (read-side shape).
type ElevationListed struct {
	Elevation
	ExerciseName string
}
