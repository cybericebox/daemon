package exercise

import (
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

type ExerciseView struct {
	ID          uuid.UUID
	Name        string
	Description string
	Tags        []string

	DraftVersionID     *uuid.UUID
	PublishedVersionID *uuid.UUID

	ArchivedAt *time.Time
	// HasChanges: the working copy differs from the published version (or
	// nothing is published yet). Drives the "Опубліковано · є зміни" badge.
	HasChanges bool

	ExerciseScopeView

	CreatedAt time.Time
	CreatedBy *uuid.UUID
	// AuthorName and UpdatedByName are the first and last name of CreatedBy and UpdatedBy ("" when unknown).
	AuthorName    string
	UpdatedAt     time.Time
	UpdatedBy     *uuid.UUID
	UpdatedByName string
}

type ExerciseListItem struct {
	ID           uuid.UUID
	Name         string
	Description  string
	Tags         []string
	HasDraft     bool
	HasPublished bool
	// Status is the single derived catalog status (exerciseModel.CatalogStatus):
	// none | draft_only | changed | published | archived.
	Status     string
	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time

	ExerciseScopeView
}

// ExerciseScopeView is the W4 ownership block shared by the card and list
// item: who owns the exercise, who may use it, where it was forked from,
// whether it has infrastructure and what the caller may do.
type ExerciseScopeView struct {
	Scope          string // catalog | event
	OwnerEventID   *uuid.UUID
	OwnerEventName string
	// OwnerEvent is OwnerEventID/OwnerEventName as one reference (nil for
	// catalog exercises and orphans of a deleted event).
	OwnerEvent  *EventRef
	AccessLevel string // all | selected | own | none
	// AccessEventIDs/AccessEvents: the "selected events" of a catalog
	// exercise, for platform readers only (empty otherwise).
	AccessEventIDs []uuid.UUID
	AccessEvents   []EventRef
	OriginEventID  *uuid.UUID
	ForkedFrom     *ExerciseForkSource
	Infrastructure bool
	// Resources is the total of the published version (min and max over its variants); zero without one.
	// ResourceHeavy: an approved elevation holds a device of the published version above the platform frame.
	Resources         ResourceRange
	ResourceHeavy     bool
	PendingProposalID *uuid.UUID
	Permissions       ExercisePermissions
}

type EventRef struct {
	ID   uuid.UUID
	Name string
}

type ExerciseForkSource struct {
	ExerciseID   uuid.UUID
	ExerciseName string
	VersionID    *uuid.UUID
}

type ExercisePermissions struct {
	CanRead         bool
	CanEdit         bool
	CanPublish      bool
	CanDelete       bool
	CanManageAccess bool
	CanPropose      bool
	CanExport       bool
}

// ProposalView is one proposal of an event exercise to the catalog.
type ProposalView struct {
	ID                uuid.UUID
	ExerciseID        uuid.UUID
	ExerciseName      string
	EventID           *uuid.UUID
	EventName         string
	Status            string // pending | approved | rejected
	Note              string
	ProposedBy        *uuid.UUID
	ProposedByName    string
	ProposedAt        time.Time
	DecidedAt         *time.Time
	DecisionNote      string
	CatalogExerciseID *uuid.UUID
}

func scopeName(s exerciseModel.Scope) string {
	if s == exerciseModel.ScopeEvent {
		return "event"
	}
	return "catalog"
}

func accessLevelName(l exerciseModel.AccessLevel) string {
	switch l {
	case exerciseModel.AccessSelectedEvents:
		return "selected"
	case exerciseModel.AccessOriginEvent:
		return "own"
	case exerciseModel.AccessNone:
		return "none"
	default:
		return "all"
	}
}

// ParseAccessLevel maps the API names back; ok is false for unknown ones.
func ParseAccessLevel(name string) (exerciseModel.AccessLevel, bool) {
	switch name {
	case "all":
		return exerciseModel.AccessAllEvents, true
	case "selected":
		return exerciseModel.AccessSelectedEvents, true
	case "own":
		return exerciseModel.AccessOriginEvent, true
	case "none":
		return exerciseModel.AccessNone, true
	}
	return 0, false
}

func toScopeView(e exerciseModel.Exercise) ExerciseScopeView {
	v := ExerciseScopeView{Scope: scopeName(e.Scope), OwnerEventID: uuidPtr(e.OwnerEventID), OriginEventID: uuidPtr(e.OriginEventID),
		AccessEventIDs: []uuid.UUID{}, AccessEvents: []EventRef{}}
	if !e.IsEventScoped() {
		v.AccessLevel = accessLevelName(e.AccessLevel)
	}
	if e.ForkedFromExerciseID.Valid {
		v.ForkedFrom = &ExerciseForkSource{ExerciseID: e.ForkedFromExerciseID.UUID, VersionID: uuidPtr(e.ForkedFromVersionID)}
	}
	return v
}

type ExercisesListResult struct {
	Exercises  []ExerciseListItem
	NextCursor uuid.UUID
	HasMore    bool
	Total      int64
}

type ExerciseTagSuggestion struct {
	Tag   string `json:"Tag"`
	Count int64  `json:"Count"`
}

// ExerciseUsage lists the events (active or archived) where any version of
// the exercise is attached.
type ExerciseUsage struct {
	Events []ExerciseUsageEvent
}

type ExerciseUsageEvent struct {
	ID       uuid.UUID
	Name     string
	Archived bool
}

func toExerciseView(e exerciseModel.Exercise) ExerciseView {
	return ExerciseView{
		ID:                 e.ID,
		Name:               e.Name,
		Description:        e.Description,
		Tags:               e.Tags,
		DraftVersionID:     uuidPtr(e.DraftVersionID),
		PublishedVersionID: uuidPtr(e.PublishedVersionID),
		ArchivedAt:         e.ArchivedAt,
		CreatedAt:          e.CreatedAt,
		CreatedBy:          uuidPtr(e.CreatedBy),
		UpdatedAt:          e.UpdatedAt,
		UpdatedBy:          uuidPtr(e.UpdatedBy),
		ExerciseScopeView:  toScopeView(e),
	}
}

func toExerciseListItem(e exerciseModel.Exercise, status exerciseModel.CatalogStatus) ExerciseListItem {
	return ExerciseListItem{
		ID: e.ID, Name: e.Name, Description: e.Description, Tags: e.Tags,
		HasDraft: e.DraftVersionID.Valid, HasPublished: e.PublishedVersionID.Valid, Status: string(status),
		ArchivedAt: e.ArchivedAt, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		ExerciseScopeView: toScopeView(e),
	}
}

func uuidPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	v := n.UUID
	return &v
}

// VersionView is one exercise version's content, secrets masked. Consumed by
// the SaveDraft/PublishDraft/RollbackToVersion/GetVersion handlers (Task 11).
type VersionView struct {
	ID         uuid.UUID
	ExerciseID uuid.UUID
	Status     string
	AdminNote  string
	Label      string

	Variants []exerciseModel.Variant // secrets masked

	VariantCount int
	CreatedAt    time.Time
	CreatedBy    *uuid.UUID
	AuthorName   string // first and last name of CreatedBy; "" when unknown
	PublishedAt  *time.Time
	// Resources: the totals of the version (min and max over its variants, per variant), the devices outside
	// the platform frame and whether the task is resource-heavy.
	Resources VersionResources
	// Elevation is the exercise's open resource elevation request, else its latest decided one; nil when it
	// never had one.
	Elevation *ElevationView
}

// VersionListItem is a version history row without the content payload.
type VersionListItem struct {
	ID           uuid.UUID
	Status       string
	AdminNote    string
	Label        string
	VariantCount int
	CreatedAt    time.Time
	CreatedBy    *uuid.UUID
	AuthorName   string // first and last name of CreatedBy; "" when unknown
	PublishedAt  *time.Time
}

// toVersionView masks secrets over a COPY of the variants (maskSecrets never
// mutates the entity the repository returned) before exposing them to callers.
func toVersionView(v exerciseModel.ExerciseVersion) VersionView {
	return VersionView{
		ID:           v.ID,
		ExerciseID:   v.ExerciseID,
		Status:       string(v.Status),
		AdminNote:    v.AdminNote,
		Label:        v.Label,
		Variants:     maskSecrets(v.Variants),
		VariantCount: len(v.Variants),
		CreatedAt:    v.CreatedAt,
		CreatedBy:    uuidPtr(v.CreatedBy),
		PublishedAt:  v.PublishedAt,
	}
}
