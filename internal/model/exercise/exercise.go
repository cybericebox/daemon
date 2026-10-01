// Package exerciseModel is the DOMAIN layer of the exercise catalog: the
// Exercise identity aggregate, the versioned content snapshot
// (ExerciseVersion → Variant → Task/Topology) and every invariant they hold.
package exerciseModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

const (
	nameMinLen        = 3
	nameMaxLen        = 50
	descriptionMaxLen = 2000
	tagMaxLen         = 30
	tagsMaxCount      = 20
)

// Scope says who owns an exercise: the platform catalog or one event.
type Scope int16

const (
	ScopeCatalog Scope = iota
	ScopeEvent
)

// AccessLevel limits which events may use a catalog exercise.
type AccessLevel int16

const (
	AccessAllEvents AccessLevel = iota
	AccessSelectedEvents
	AccessOriginEvent
	// AccessNone: no event may attach, fork or see the exercise. Events that
	// attached it before keep their pinned versions (never re-checked) but
	// cannot switch to a newer one.
	AccessNone
)

func (l AccessLevel) Valid() bool {
	return l == AccessAllEvents || l == AccessSelectedEvents || l == AccessOriginEvent || l == AccessNone
}

// CatalogStatus is the single status of an exercise in catalog lists, derived
// in SQL (exercise_status) from the version pointers and the archive state.
type CatalogStatus string

const (
	StatusNone      CatalogStatus = "none"       // nothing saved nor published
	StatusDraftOnly CatalogStatus = "draft_only" // a working copy, never published
	StatusChanged   CatalogStatus = "changed"    // published; the working copy differs
	StatusPublished CatalogStatus = "published"
	StatusArchived  CatalogStatus = "archived"
)

func (s CatalogStatus) Valid() bool {
	switch s {
	case StatusNone, StatusDraftOnly, StatusChanged, StatusPublished, StatusArchived:
		return true
	}
	return false
}

// Exercise is the catalog identity aggregate. Version content lives in
// ExerciseVersion; the two pointers below are maintained ONLY by the
// lifecycle CTE queries, never by identity mutations.
type Exercise struct {
	ID          uuid.UUID
	Name        string
	Description string
	Tags        []string

	DraftVersionID     uuid.NullUUID
	PublishedVersionID uuid.NullUUID

	// Scope/OwnerEventID: an event-scoped exercise belongs to one event; the
	// owner becomes NULL when that event is deleted (the row is archived).
	Scope        Scope
	OwnerEventID uuid.NullUUID
	// AccessLevel/OriginEventID apply to catalog exercises only; the origin
	// is the event a proposal came from ("only its own event").
	AccessLevel   AccessLevel
	OriginEventID uuid.NullUUID
	// ForkedFrom*: an event copy of another exercise remembers its source.
	ForkedFromExerciseID uuid.NullUUID
	ForkedFromVersionID  uuid.NullUUID

	// ArchivedAt marks the entry hidden from the catalog and frozen for edits;
	// nil = active. Existing event attachments keep working. Catalog-local
	// state: never part of the portable archive, an import is always active.
	ArchivedAt *time.Time `json:"-"`

	CreatedAt time.Time
	CreatedBy uuid.NullUUID
	UpdatedAt time.Time
	UpdatedBy uuid.NullUUID
}

// NewExercise builds a catalog entry with domain-owned defaults (UUIDv7 id,
// timestamps from the caller's clock).
func NewExercise(name, description string, tags []string, createdBy uuid.UUID, now time.Time) (Exercise, error) {
	e := Exercise{
		ID:        uuid.Must(uuid.NewV7()),
		CreatedAt: now,
		CreatedBy: uuid.NullUUID{UUID: createdBy, Valid: createdBy != uuid.Nil},
	}
	if err := e.UpdateIdentity(name, description, tags, createdBy, now); err != nil {
		return Exercise{}, err
	}
	return e, nil
}

// NewEventExercise builds an exercise owned by one event.
func NewEventExercise(name, description string, tags []string, eventID, createdBy uuid.UUID, now time.Time) (Exercise, error) {
	e, err := NewExercise(name, description, tags, createdBy, now)
	if err != nil {
		return Exercise{}, err
	}
	e.Scope = ScopeEvent
	e.OwnerEventID = uuid.NullUUID{UUID: eventID, Valid: eventID != uuid.Nil}
	return e, nil
}

// NewFork builds the event-owned copy of source ("Налаштувати під захід"): same
// identity texts, remembering the source exercise and version.
func NewFork(source Exercise, sourceVersionID, eventID, createdBy uuid.UUID, now time.Time) (Exercise, error) {
	e, err := NewEventExercise(source.Name, source.Description, source.Tags, eventID, createdBy, now)
	if err != nil {
		return Exercise{}, err
	}
	e.ForkedFromExerciseID = uuid.NullUUID{UUID: source.ID, Valid: true}
	e.ForkedFromVersionID = uuid.NullUUID{UUID: sourceVersionID, Valid: sourceVersionID != uuid.Nil}
	return e, nil
}

// NewCatalogCopy builds the catalog exercise an approved proposal creates
// from an event exercise.
func NewCatalogCopy(source Exercise, name string, level AccessLevel, hasSelection bool, createdBy uuid.UUID, now time.Time) (Exercise, error) {
	if name == "" {
		name = source.Name
	}
	e, err := NewExercise(name, source.Description, source.Tags, createdBy, now)
	if err != nil {
		return Exercise{}, err
	}
	e.OriginEventID = source.OwnerEventID
	if err = e.SetAccess(level, hasSelection, createdBy, now); err != nil {
		return Exercise{}, err
	}
	return e, nil
}

// IsEventScoped reports whether the exercise belongs to an event (possibly an
// orphan whose event was deleted).
func (e *Exercise) IsEventScoped() bool { return e.Scope == ScopeEvent }

// SetAccess changes which events may use a catalog exercise. hasSelection
// tells whether the selected-events list is non-empty; "origin event only"
// needs an origin event; "no event" needs nothing.
func (e *Exercise) SetAccess(level AccessLevel, hasSelection bool, by uuid.UUID, now time.Time) error {
	if e.Scope != ScopeCatalog || !level.Valid() ||
		(level == AccessSelectedEvents && !hasSelection) ||
		(level == AccessOriginEvent && !e.OriginEventID.Valid) {
		return ErrExerciseAccessInvalid.Err()
	}
	e.AccessLevel = level
	e.touch(by, now)
	return nil
}

// UpdateIdentity validates and applies name/description/tags in one place and
// touches UpdatedAt/UpdatedBy. On validation failure the entity is untouched.
func (e *Exercise) UpdateIdentity(name, description string, tags []string, updatedBy uuid.UUID, now time.Time) error {
	if err := e.EnsureNotArchived(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if len(name) < nameMinLen || len(name) > nameMaxLen {
		return ErrExerciseNameInvalid.Err()
	}
	if len(description) > descriptionMaxLen {
		return ErrExerciseDescriptionTooLong.Err()
	}
	normalized, ok := normalizeTags(tags)
	if !ok {
		return ErrExerciseTagsInvalid.Err()
	}

	e.Name = name
	e.Description = description
	e.Tags = normalized
	e.touch(updatedBy, now)
	return nil
}

// Archive hides the exercise from the catalog and freezes it. Idempotent: an
// already archived exercise keeps its original ArchivedAt and UpdatedAt.
func (e *Exercise) Archive(by uuid.UUID, now time.Time) {
	if e.ArchivedAt != nil {
		return
	}
	archivedAt := now
	e.ArchivedAt = &archivedAt
	e.touch(by, now)
}

// Unarchive returns the exercise to the catalog. Idempotent.
func (e *Exercise) Unarchive(by uuid.UUID, now time.Time) {
	if e.ArchivedAt == nil {
		return
	}
	e.ArchivedAt = nil
	e.touch(by, now)
}

// EnsureNotArchived is the single guard for "archived exercises are frozen":
// identity and content mutations, and attaching/replacing on events.
func (e *Exercise) EnsureNotArchived() error {
	if e.ArchivedAt != nil {
		return ErrExerciseArchived.Err()
	}
	return nil
}

// EnsureNotInUse refuses deleting an exercise that any event — active or
// archived — still references; usedBy names those events.
func EnsureNotInUse(usedBy []string) error {
	if len(usedBy) == 0 {
		return nil
	}
	return ErrExerciseInUse.WithContext("events", usedBy).Err()
}

// HasUnpublishedChanges reports whether the working copy differs from the
// published version. Nothing published yet → true; published and no draft row
// (the working copy is the published content, materialized lazily) → false.
// Only when both exist is the expensive content comparison consulted.
func (e *Exercise) HasUnpublishedChanges(contentDiffers func() (bool, error)) (bool, error) {
	if !e.PublishedVersionID.Valid {
		return true, nil
	}
	if !e.DraftVersionID.Valid {
		return false, nil
	}
	return contentDiffers()
}

func (e *Exercise) touch(by uuid.UUID, now time.Time) {
	e.UpdatedAt = now
	e.UpdatedBy = uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
}

// normalizeTags trims, lowercases and deduplicates tags; reports false on any
// empty/oversized tag or when there are too many.
func normalizeTags(tags []string) ([]string, bool) {
	if len(tags) > tagsMaxCount {
		return nil, false
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || len(tag) > tagMaxLen {
			return nil, false
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out, true
}
