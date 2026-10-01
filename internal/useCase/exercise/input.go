package exercise

import (
	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

type CreateExerciseInput struct {
	Name        string
	Description string
	Tags        []string
	CreatedBy   uuid.UUID
	// OwnerEventID: nil creates a catalog exercise, else one owned by that
	// event.
	OwnerEventID *uuid.UUID
}

// SetAccessInput changes which events may use a catalog exercise.
type SetAccessInput struct {
	AccessLevel exerciseModel.AccessLevel
	EventIDs    []uuid.UUID
	UpdatedBy   uuid.UUID
}

// ApproveProposalInput: Name overrides the copy's name (empty = source name).
type ApproveProposalInput struct {
	Name        string
	AccessLevel exerciseModel.AccessLevel
	EventIDs    []uuid.UUID
	Note        string
}

type UpdateExerciseInput struct {
	ID          uuid.UUID
	Name        string
	Description string
	Tags        []string
	UpdatedBy   uuid.UUID
}

type ExercisesFilter struct {
	Search   string
	Tags     []string
	Status   string
	Archived string // "only"; anything else = "exclude"
	Cursor   uuid.UUID
	Page     int
	PageSize int
	SortBy   string
	SortDir  string
	// W4 visibility filters: Scope '' | catalog | event; EventIDs keeps what
	// is relevant to ANY of the events; Infrastructure '' | yes | no.
	Scope          string
	EventIDs       []uuid.UUID
	Infrastructure string
}
