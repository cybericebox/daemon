// Package eventExerciseModel owns the immutable link from an event to one
// published catalog version. Future catalogue edits never alter this link.
package eventExerciseModel

import (
	"time"

	"github.com/gofrs/uuid"
)

type VariantMode int16

const (
	VariantModePerTeam VariantMode = iota
	VariantModeFixed
)

func (m VariantMode) valid() bool { return m == VariantModePerTeam || m == VariantModeFixed }

// Status describes whether a bundle revision may be assigned to newly
// starting teams. Superseded revisions remain immutable evidence for teams
// already using them.
type Status int16

const (
	StatusActive Status = iota
	StatusSuperseded
	// StatusDetached: removed from the event after teams attempted it; kept
	// as evidence, off every board and score.
	StatusDetached
)

type EventExercise struct {
	ID                uuid.UUID
	EventID           uuid.UUID
	ExerciseID        uuid.UUID
	ExerciseVersionID uuid.UUID
	VariantMode       VariantMode
	FixedVariantIndex *int32
	Revision          int32
	Status            Status
	ReplacesID        *uuid.UUID
	SupersededAt      *time.Time
	DetachedAt        *time.Time
	CreatedAt         time.Time
	CreatedBy         uuid.NullUUID
}

func New(eventID, exerciseID, versionID uuid.UUID, mode VariantMode, fixedVariantIndex *int32, now time.Time, by uuid.UUID) (EventExercise, error) {
	if eventID == uuid.Nil || exerciseID == uuid.Nil || versionID == uuid.Nil {
		return EventExercise{}, ErrEventExerciseIdentityInvalid.Err()
	}
	if !mode.valid() || (mode == VariantModeFixed && (fixedVariantIndex == nil || *fixedVariantIndex < 0)) || (mode == VariantModePerTeam && fixedVariantIndex != nil) {
		return EventExercise{}, ErrEventExerciseVariantModeInvalid.Err()
	}
	var fixed *int32
	if fixedVariantIndex != nil {
		value := *fixedVariantIndex
		fixed = &value
	}
	return EventExercise{ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: versionID, VariantMode: mode, FixedVariantIndex: fixed, Revision: 1, Status: StatusActive, CreatedAt: now, CreatedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}}, nil
}

// NewReplacement builds the next immutable revision for the same catalog
// exercise. It deliberately cannot change event identity or the participation
// variant policy while correcting content.
func NewReplacement(previous EventExercise, versionID uuid.UUID, now time.Time, by uuid.UUID) (EventExercise, error) {
	if previous.Status != StatusActive {
		return EventExercise{}, ErrEventExerciseNotActive.Err()
	}
	next, err := New(previous.EventID, previous.ExerciseID, versionID, previous.VariantMode, previous.FixedVariantIndex, now, by)
	if err != nil {
		return EventExercise{}, err
	}
	next.Revision = previous.Revision + 1
	next.ReplacesID = &previous.ID
	return next, nil
}

// EnsureActive guards every change of an attachment: only the active one of
// an event may switch source, be detached or have its board edited.
func (e EventExercise) EnsureActive() error {
	if e.Status != StatusActive {
		return ErrEventExerciseNotActive.Err()
	}
	return nil
}

// CanonicalVariant is the variant whose task texts label the event board: the
// fixed variant when the attachment pins one, else the first.
func (e EventExercise) CanonicalVariant(variantCount int) int {
	if e.VariantMode == VariantModeFixed && e.FixedVariantIndex != nil && int(*e.FixedVariantIndex) < variantCount {
		return int(*e.FixedVariantIndex)
	}
	return 0
}

// DetachDecision says how a detach proceeds: without attempts the attachment
// is deleted; with attempts it needs an explicit confirmation and is kept as
// detached.
func DetachDecision(hasAttempts, confirmed bool) (keep bool, err error) {
	if !hasAttempts {
		return false, nil
	}
	if !confirmed {
		return false, ErrEventExerciseDetachNeedsConfirm.Err()
	}
	return true, nil
}
