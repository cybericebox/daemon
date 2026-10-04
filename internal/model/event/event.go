// Package eventModel is the DOMAIN layer of the platform event: the thin
// Event tenancy aggregate (subdomain tag + a two-date availability window)
// and the invariants it holds. Rich event content lives in the per-event
// domain, not here.
package eventModel

import (
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

const (
	tagMinLen  = 3
	tagMaxLen  = 64
	nameMaxLen = 255
)

var tagPattern = regexp.MustCompile(`^[a-z0-9]+$`)

// reservedTags are the labels of the platform's own subdomains (the app hosts and www): an event can never take
// one of them as its tag, so <tag>.DOMAIN never collides with them. extraReservedTags are the labels other
// components own (the laboratory names labs, vpn, ctl): the deployment names them (EVENT_RESERVED_TAGS_EXTRA).
var (
	reservedTags      = map[string]bool{"api": true, "id": true, "admin": true, "exercises": true, "www": true}
	extraReservedTags = map[string]bool{}
)

// SetExtraReservedTags replaces the deployment's extra reserved tags. Call it once at start, before serving.
func SetExtraReservedTags(tags []string) {
	extra := make(map[string]bool, len(tags))
	for _, t := range tags {
		extra[t] = true
	}
	extraReservedTags = extra
}

// IsReservedTag reports whether tag is a fixed or extra reserved label.
func IsReservedTag(tag string) bool { return reservedTags[tag] || extraReservedTags[tag] }

// ReservedTags returns the reserved event tags, fixed and extra (a copy).
func ReservedTags() map[string]bool {
	tags := make(map[string]bool, len(reservedTags)+len(extraReservedTags))
	for t := range reservedTags {
		tags[t] = true
	}
	for t := range extraReservedTags {
		tags[t] = true
	}
	return tags
}

// EventStatus is the read-only lifecycle position derived from the window.
type EventStatus int32

const (
	EventPendingStatus EventStatus = iota
	EventActiveStatus
	EventArchivedStatus
)

// Event is the platform tenancy aggregate. The platform owns ONLY the tag,
// an optional admin name and the availability window; everything else about
// the event is configured in the per-event domain.
type Event struct {
	ID           uuid.UUID
	Tag          string // event subdomain
	Name         string // public label, changed by event managers
	InternalName string // platform-only label, changed by platform administrators
	// Lifecycle is the canonical runtime model. The availability window below
	// is retained only while the central-admin API is migrated.
	Lifecycle         Lifecycle
	ScoringProfile    ScoringProfile
	ForceEventScoring bool
	// StaticPoints is the one value of static event scoring (required for
	// static scoring; tasks «Як у заходу» and forced scoring use it).
	StaticPoints *int32
	// InfrastructureAllowed is the platform administrator's decision, made
	// whether the event may use challenges with team stands. It is set at
	// creation and may be changed only before publication (SetInfrastructureAllowed).
	InfrastructureAllowed bool

	AvailableFrom time.Time // domain becomes available to moderators/users
	ArchiveAt     time.Time // zero means platform access has no planned end

	CreatedAt time.Time
	CreatedBy uuid.NullUUID
	UpdatedAt time.Time
	UpdatedBy uuid.NullUUID
}

// NewEvent builds a platform event with domain-owned defaults (UUIDv7 id,
// timestamps from the caller's clock). A zero availableFrom defaults to now.
func NewEvent(tag, name string, availableFrom, archiveAt time.Time, createdBy uuid.UUID, now time.Time) (Event, error) {
	e := Event{
		ID:        uuid.Must(uuid.NewV7()),
		CreatedAt: now,
		CreatedBy: uuid.NullUUID{UUID: createdBy, Valid: createdBy != uuid.Nil},
	}
	if availableFrom.IsZero() {
		availableFrom = now
	}
	if err := e.UpdateEvent(tag, name, availableFrom, archiveAt, createdBy, now); err != nil {
		return Event{}, err
	}
	e.Name = e.InternalName
	return e, nil
}

// AllowInfrastructure records the creation-time infrastructure decision. It
// has effect only before the first persist: the event update write never
// changes the stored flag.
func (e *Event) AllowInfrastructure(allowed bool) {
	e.InfrastructureAllowed = allowed
}

// SetInfrastructureAllowed changes the administrator's infrastructure flag. It
// is refused once the event is published, and turning it off is refused while
// attached sets still need infrastructure (attachedInfrastructure counts them):
// detaching is the manager's decision, never a silent side effect. Setting the
// current value is a no-op.
func (e *Event) SetInfrastructureAllowed(allowed bool, attachedInfrastructure int, updatedBy uuid.UUID, now time.Time) error {
	if e.InfrastructureAllowed == allowed {
		return nil
	}
	if e.Lifecycle.Status(now) != LifecycleNotPublished {
		return ErrEventInfrastructureLocked.Err()
	}
	if !allowed && attachedInfrastructure > 0 {
		return ErrEventInfrastructureInUse.Err()
	}
	e.InfrastructureAllowed = allowed
	e.UpdatedAt = now
	e.UpdatedBy = uuid.NullUUID{UUID: updatedBy, Valid: updatedBy != uuid.Nil}
	return nil
}

// UpdateEvent validates and applies tag/name/window in one place and touches
// UpdatedAt/By. On validation failure the entity is untouched.
func (e *Event) UpdateEvent(tag, name string, availableFrom, archiveAt time.Time, updatedBy uuid.UUID, now time.Time) error {
	tag = strings.TrimSpace(tag)
	if len(tag) < tagMinLen || len(tag) > tagMaxLen || !tagPattern.MatchString(tag) {
		return ErrEventTagInvalid.Err()
	}
	// A tag that is already the event's own stays editable (an event created before the reservation keeps working).
	if IsReservedTag(tag) && tag != e.Tag {
		return ErrEventTagReserved.Err()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrEventNameInvalid.Err()
	}
	if len(name) > nameMaxLen {
		return ErrEventNameTooLong.Err()
	}
	if !archiveAt.IsZero() && !archiveAt.After(availableFrom) {
		return ErrEventDatesInvalid.Err()
	}
	if e.Lifecycle.Configured {
		if e.Lifecycle.PublishAt.Before(availableFrom) ||
			(!archiveAt.IsZero() && (!e.Lifecycle.StartAt.Before(archiveAt) ||
				(e.Lifecycle.WithdrawAt != nil && e.Lifecycle.WithdrawAt.After(archiveAt)))) {
			return ErrEventDatesInvalid.Err()
		}
	}
	// Platform updates never overwrite a moderator's configured lifecycle.
	unconfigured := !e.Lifecycle.Configured
	var lifecycle Lifecycle
	if unconfigured {
		lifecycle = Lifecycle{JoinPolicy: JoinPolicyLockedAtStart, PublishAt: availableFrom, StartAt: availableFrom}
	}

	e.Tag = tag
	e.InternalName = name
	e.AvailableFrom = availableFrom
	e.ArchiveAt = archiveAt
	if unconfigured {
		e.Lifecycle = lifecycle
	}
	e.UpdatedAt = now
	e.UpdatedBy = uuid.NullUUID{UUID: updatedBy, Valid: updatedBy != uuid.Nil}
	return nil
}

// UpdatePublicName does not touch the platform administrator's label.
func (e *Event) UpdatePublicName(name string, updatedBy uuid.UUID, now time.Time) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrEventNameInvalid.Err()
	}
	if len(name) > nameMaxLen {
		return ErrEventNameTooLong.Err()
	}
	e.Name = name
	e.UpdatedAt = now
	e.UpdatedBy = uuid.NullUUID{UUID: updatedBy, Valid: updatedBy != uuid.Nil}
	return nil
}

// Archive collapses the window end to `now` (early cancel). For an event that
// has not started, the canonical lifecycle is compressed to a one-microsecond
// run before withdrawal; the original availability date remains historical.
func (e *Event) Archive(now time.Time, updatedBy uuid.UUID) {
	e.ArchiveAt = now
	startAt := e.Lifecycle.StartAt
	if !startAt.Before(now) {
		startAt = now.Add(-time.Microsecond)
	}
	publishAt := e.Lifecycle.PublishAt
	if publishAt.After(startAt) {
		publishAt = startAt
	}
	finishAt := now
	withdrawAt := now.Add(time.Microsecond)
	if lifecycle, err := NewLifecycle(e.Lifecycle.JoinPolicy,
		publishAt, startAt, &finishAt, &withdrawAt, nil); err == nil {
		e.Lifecycle = lifecycle
	}
	e.UpdatedAt = now
	e.UpdatedBy = uuid.NullUUID{UUID: updatedBy, Valid: updatedBy != uuid.Nil}
}

// UpdateLifecycle replaces the canonical runtime configuration. Callers build
// the Lifecycle through NewLifecycle first, so this method cannot admit an
// invalid combination of schedule and timestamps.
func (e *Event) UpdateLifecycle(lifecycle Lifecycle, updatedBy uuid.UUID, now time.Time) {
	e.Lifecycle = lifecycle
	e.UpdatedAt = now
	e.UpdatedBy = uuid.NullUUID{UUID: updatedBy, Valid: updatedBy != uuid.Nil}
}

func (e *Event) UpdateScoringProfile(profile ScoringProfile, force bool, staticPoints *int32, updatedBy uuid.UUID, now time.Time) error {
	// Static scoring needs its one value; dynamic scoring keeps any value.
	if (profile.Mode == ScoringStatic && staticPoints == nil) || (staticPoints != nil && *staticPoints <= 0) {
		return ErrEventStaticPointsInvalid.Err()
	}
	e.ScoringProfile = profile
	e.ForceEventScoring = force
	e.StaticPoints = staticPoints
	e.UpdatedAt = now
	e.UpdatedBy = uuid.NullUUID{UUID: updatedBy, Valid: updatedBy != uuid.Nil}
	return nil
}

func legacyWindowLifecycle(availableFrom, archiveAt time.Time) (Lifecycle, error) {
	finishAt := archiveAt
	withdrawAt := archiveAt.Add(time.Microsecond)
	return NewLifecycle(JoinPolicyLockedAtStart, availableFrom, availableFrom, &finishAt, &withdrawAt, nil)
}

// Status derives the lifecycle position. Archived is checked first so an
// early Archive (ArchiveAt moved to now, possibly before AvailableFrom) reads
// as Archived rather than Pending.
func (e *Event) Status(now time.Time) EventStatus {
	if !e.ArchiveAt.IsZero() && !now.Before(e.ArchiveAt) {
		return EventArchivedStatus
	}
	if now.Before(e.AvailableFrom) {
		return EventPendingStatus
	}
	return EventActiveStatus
}
