package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// fieldStatus is the column or filter field name of that table.
const fieldStatus = "status"

var maxUUID = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
var cursorSentinelTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// MaxBulkEvents bounds one bulk request on events.
const MaxBulkEvents = 200

// CreateEvent persists a new platform event. Route gate: events.write.
func (u *EventUseCase) CreateEvent(ctx context.Context, in CreateEventInput) (EventView, error) {
	now := time.Now()
	e, err := eventModel.NewEvent(in.Tag, in.Name, in.AvailableFrom, in.ArchiveAt, in.CreatedBy, now)
	if err != nil {
		return EventView{}, err
	}
	var source eventModel.Event
	if in.CopyFromEventID != uuid.Nil {
		if source, err = u.events.GetByID(ctx, in.CopyFromEventID); err != nil {
			if repositoryTools.IsObjectNotFoundError(err) {
				return EventView{}, eventModel.ErrEventNotFound.Err()
			}
			return EventView{}, model.ErrPlatform.WithError(err).
				WithMessage("Failed to get the event to copy").
				Err()
		}
		if in.InfrastructureAllowed == nil {
			copyInfra := source.InfrastructureAllowed
			in.InfrastructureAllowed = &copyInfra
		}
	}
	allowed, err := u.creationInfrastructureAllowed(ctx, in.InfrastructureAllowed)
	if err != nil {
		return EventView{}, err
	}
	if in.CopyFromEventID != uuid.Nil && !allowed {
		// The copy would carry laboratory tasks the event cannot run.
		if infra, countErr := u.eventExercises.CountInfrastructure(
			ctx,
			in.CopyFromEventID,
		); countErr != nil {
			return EventView{}, model.ErrPlatform.WithError(countErr).
				WithMessage("Failed to count infrastructure exercises").
				Err()
		} else if infra > 0 {
			return EventView{}, eventModel.ErrEventInfrastructureUnavailable.Err()
		}
	}
	e.AllowInfrastructure(allowed)
	liveCount, err := u.events.CountOverlappingWithTag(ctx, e.Tag, e.AvailableFrom, e.ArchiveAt, uuid.Nil)
	if err != nil {
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check tag uniqueness").Err()
	}
	if liveCount > 0 {
		return EventView{}, eventModel.ErrEventExists.Err()
	}
	if u.uow == nil {
		return EventView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventView{}, err
	}
	defer unit.Restore()
	created, err := eventRepo.New(txRepo).Create(txCtx, e)
	if err != nil {
		return EventView{}, classifyEventWriteError(err, "create")
	}
	if _, err = eventConfigRepo.New(txRepo).Create(txCtx, eventConfigModel.NewEventConfig(created.ID, now)); err != nil {
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event config").Err()
	}
	owner, err := eventManagerModel.New(created.ID, in.CreatedBy, eventManagerModel.RoleOwner, now)
	if err != nil {
		return EventView{}, err
	}
	if _, err = eventManagerRepo.New(txRepo).Create(txCtx, owner); err != nil {
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event owner").Err()
	}
	var ownVersions []exerciseModel.ExerciseVersion
	if in.CopyFromEventID != uuid.Nil {
		if ownVersions, err = u.copyEventContent(
			txCtx,
			txRepo,
			source,
			created,
			in.CreatedBy,
			now,
		); err != nil {
			return EventView{}, err
		}
	}
	if err = unit.Save(); err != nil {
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event").Err()
	}
	if in.CopyFromEventID != uuid.Nil {
		u.copyEventMedia(ctx, in.CopyFromEventID, created.ID, ownVersions)
	}
	u.tagsChanged()
	return toEventView(created, now), nil
}

// creationInfrastructureAllowed resolves the admin's infrastructure choice:
// omitted means «allowed when Laboratory is available»; an explicit request
// needs Laboratory now, because the flag can never be changed later.
func (u *EventUseCase) creationInfrastructureAllowed(ctx context.Context, requested *bool) (bool, error) {
	available := u.infrastructureCapability != nil && u.infrastructureCapability.RequireLaboratories(ctx) == nil
	if requested == nil {
		return available, nil
	}
	if *requested && !available {
		return false, eventModel.ErrEventInfrastructureUnavailable.Err()
	}
	return *requested, nil
}

// GetEvent returns the event by ID. Route gate: events.read.
func (u *EventUseCase) GetEvent(ctx context.Context, id uuid.UUID) (EventView, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	view := toEventView(e, time.Now())
	if counts, countErr := u.events.CountsOf(ctx, []uuid.UUID{id}); countErr == nil {
		c := counts[id]
		view.Counts = &EventCountsView{
			Participants: c.Participants,
			Teams:        c.Teams,
			StandsReady:  c.StandsReady,
			StandsTotal:  c.StandsTotal,
		}
	}
	return view, nil
}

// ResolveEventByTag resolves the live tenant event for a subdomain tag —
// the single non-archived event sharing the tag as of now. Consumed by the
// Origin-based tenant-resolution middleware (via its own adapter; this
// package must not import that delivery package). Not route-gated: it backs
// participant-facing tenant resolution, not an admin route.
func (u *EventUseCase) ResolveEventByTag(ctx context.Context, tag string, now time.Time) (EventTenantView, error) {
	e, err := u.events.GetLiveByTag(ctx, tag, now)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventTenantView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventTenantView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to resolve event by tag").Err()
	}
	return toEventTenantView(e, now), nil
}

// EventTagExists reports whether an event with the tag exists in any lifecycle
// state (archived included; a deleted event is gone). It backs the browser
// origin allow-list: an event site may call the API for as long as its event
// exists. Not route-gated.
func (u *EventUseCase) EventTagExists(ctx context.Context, tag string) (bool, error) {
	ok, err := u.events.TagExists(ctx, tag)
	if err != nil {
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to check the event tag").Err()
	}
	return ok, nil
}

// TagListener is told when the set of event tags may have changed (an event was
// created, deleted or retagged), so caches keyed by tag can drop their entries.
type TagListener interface {
	Invalidate()
}

// SetTagListener wires the listener; nil until wired.
func (u *EventUseCase) SetTagListener(l TagListener) { u.tagListener = l }

func (u *EventUseCase) tagsChanged() {
	if u.tagListener != nil {
		u.tagListener.Invalidate()
	}
}

// ListEvents returns a keyset page filtered by search. Route gate: events.read.
func (u *EventUseCase) ListEvents(ctx context.Context, f ListEventsFilter) (EventsListResult, error) {
	now := time.Now()
	limit := f.PageSize
	if limit <= 0 || limit > pagination.MaxPageSize {
		limit = pagination.DefaultPageSize
	}
	if f.Page > 0 {
		sortBy := f.SortBy
		switch sortBy {
		case "tag",
			"name",
			fieldStatus,
			"availableFrom",
			"archiveAt",
			"updated",
			"start",
			"participants",
			"teams",
			"stands":
		default:
			sortBy = "created"
		}
		sortDir := f.SortDir
		if sortDir != "asc" {
			sortDir = "desc"
		}
		offset := (int64(f.Page) - 1) * int64(limit)
		if offset < 0 || offset > 2147483647 {
			return EventsListResult{}, model.ErrPlatform.WithMessage("Event page is out of range").Err()
		}
		params := eventRepo.PageParams{
			Search:           f.Search,
			Status:           f.Status,
			SortBy:           sortBy,
			SortDir:          sortDir,
			Now:              now,
			Limit:            int32(limit),
			Offset:           int32(offset),
			StartFrom:        f.StartFrom,
			StartTo:          f.StartTo,
			Infrastructure:   f.Infrastructure,
			NeedsReservation: f.NeedsReservation,
			OrganizerID:      uuid.NullUUID{UUID: f.OrganizerID, Valid: f.OrganizerID != uuid.Nil},
		}
		rows, err := u.events.ListPage(ctx, params)
		if err != nil {
			return EventsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list events").Err()
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, e := range rows {
			ids = append(ids, e.ID)
		}
		counts, err := u.events.CountsOf(ctx, ids)
		if err != nil {
			return EventsListResult{}, model.ErrPlatform.WithError(err).
				WithMessage("Failed to count event participants").
				Err()
		}
		items := make([]EventView, 0, len(rows))
		for _, e := range rows {
			view := toEventView(e, now)
			c := counts[e.ID]
			view.Counts = &EventCountsView{
				Participants: c.Participants,
				Teams:        c.Teams,
				StandsReady:  c.StandsReady,
				StandsTotal:  c.StandsTotal,
			}
			items = append(items, view)
		}
		total, err := u.events.CountPage(ctx, params)
		if err != nil {
			return EventsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count events").Err()
		}
		return EventsListResult{Events: items, Total: total}, nil
	}
	curTs, curID := cursorSentinelTime, maxUUID
	if f.Cursor != uuid.Nil {
		if e, err := u.events.GetByID(ctx, f.Cursor); err == nil {
			curTs, curID = e.CreatedAt, e.ID
		}
	}
	events, err := u.events.ListCursor(ctx, eventRepo.ListParams{
		Search:          f.Search,
		CursorCreatedAt: curTs,
		CursorID:        curID,
		Limit:           int32(limit + 1),
	})
	if err != nil {
		return EventsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list events").Err()
	}
	hasMore := false
	if len(events) > limit {
		hasMore = true
		events = events[:limit]
	}
	items := make([]EventView, 0, len(events))
	for _, e := range events {
		items = append(items, toEventView(e, now))
	}
	next := uuid.Nil
	if hasMore && len(events) > 0 {
		next = events[len(events)-1].ID
	}
	total, err := u.events.Count(ctx, f.Search)
	if err != nil {
		return EventsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count events").Err()
	}
	return EventsListResult{Events: items, NextCursor: next, HasMore: hasMore, Total: total}, nil
}

// UpdateEvent updates tag/name/window via the canonical mutate path. Route
// gate: events.write.
func (u *EventUseCase) UpdateEvent(ctx context.Context, id uuid.UUID, in UpdateEventInput, updatedBy uuid.UUID) (EventView, error) {
	now := time.Now()
	// The domain (Event.UpdateEvent) trims the tag before persisting, so the
	// guard must normalize the same way — otherwise an untrimmed tag misses
	// the exact `tag = $1` match against the stored (trimmed) value and a
	// live-tag collision slips through.
	trimmedTag := strings.TrimSpace(in.Tag)
	liveCount, err := u.events.CountOverlappingWithTag(ctx, trimmedTag, in.AvailableFrom, in.ArchiveAt, id)
	if err != nil {
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check tag uniqueness").Err()
	}
	if liveCount > 0 {
		return EventView{}, eventModel.ErrEventExists.Err()
	}
	e, err := u.mutateEvent(ctx, id, func(ev *eventModel.Event) error {
		return ev.UpdateEvent(in.Tag, in.Name, in.AvailableFrom, in.ArchiveAt, updatedBy, now)
	})
	if err != nil {
		return EventView{}, err
	}
	u.tagsChanged()
	return toEventView(e, now), nil
}

// GetEventPublicName is scoped to event managers by the delivery gate.
func (u *EventUseCase) GetEventPublicName(ctx context.Context, id uuid.UUID) (string, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return "", eventModel.ErrEventNotFound.Err()
		}
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get event name").Err()
	}
	return e.Name, nil
}

// UpdateEventPublicName changes only the participant-visible label.
func (u *EventUseCase) UpdateEventPublicName(ctx context.Context, id uuid.UUID, name string, updatedBy uuid.UUID) (string, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return "", eventModel.ErrEventNotFound.Err()
		}
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	expected := e.UpdatedAt
	if err = e.UpdatePublicName(name, updatedBy, time.Now()); err != nil {
		return "", err
	}
	affected, err := u.events.UpdatePublicName(ctx, e, expected)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to update event name").Err()
	}
	if affected == 0 {
		if _, err = u.events.GetByID(ctx, id); err != nil {
			return "", eventModel.ErrEventNotFound.Err()
		}
		return "", eventModel.ErrEventModified.Err()
	}
	return e.Name, nil
}

// ArchiveEvent collapses the event's window end to now (early cancel). Route
// gate: events.write.
func (u *EventUseCase) ArchiveEvent(ctx context.Context, id uuid.UUID, updatedBy uuid.UUID) (EventView, error) {
	now := time.Now()
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if e.Status(now) == eventModel.EventArchivedStatus {
		return toEventView(e, now), nil
	}
	expectedUpdatedAt := e.UpdatedAt
	e.Archive(now, updatedBy)
	affected, err := u.events.Archive(ctx, e, expectedUpdatedAt)
	if err != nil {
		return EventView{}, classifyEventWriteError(err, "archive")
	}
	if affected == 0 {
		if _, err = u.events.GetByID(ctx, id); err != nil {
			return EventView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventView{}, eventModel.ErrEventModified.Err()
	}
	return toEventView(e, now), nil
}

// DeleteEvent removes the platform event. The exercises it owns are archived
// in the same transaction (never deleted); the owner pointer is then nulled by
// the foreign key. Route gate: events.write.
func (u *EventUseCase) DeleteEvent(ctx context.Context, id uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if err = exerciseRepo.New(txRepo).ArchiveOwnedBy(txCtx, id, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to archive event exercises").Err()
	}
	// Queue the teardown of every team LabGroup first, in the same transaction:
	// the request survives the event row, so a crash cannot orphan a stand.
	if _, err = labBindingRepo.New(txRepo).QueueEventCleanup(txCtx, id, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to queue event laboratory cleanup").Err()
	}
	affected, err := eventRepo.New(txRepo).Delete(txCtx, id)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event").Err()
	}
	if affected == 0 {
		return eventModel.ErrEventNotFound.Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event").Err()
	}
	u.wakeLabCleanup(ctx)
	u.cancelEventNotices(ctx, id)
	u.tagsChanged()
	return nil
}

// EventFacetFields are the fields of the events table that have a facet.
func EventFacetFields() []string { return []string{fieldStatus, "infrastructure", "organizer"} }

// EventFacets counts the events per value of one field under the other filters of the table (the field's own filter
// is ignored). Route gate: events.read.
func (u *EventUseCase) EventFacets(
	ctx context.Context,
	field string,
	f ListEventsFilter,
) (map[string]int64, error) {
	if !slices.Contains(EventFacetFields(), field) {
		return nil, eventModel.ErrEventLifecycleInvalid.WithMessage("Unknown facet field").Err()
	}
	switch field {
	case fieldStatus:
		f.Status = ""
	case "infrastructure":
		f.Infrastructure = ""
	case "organizer":
		f.OrganizerID = uuid.Nil
	}
	counts, err := u.events.Facet(ctx, field, eventRepo.PageParams{
		Search:    f.Search,
		Status:    f.Status,
		Now:       time.Now(),
		StartFrom: f.StartFrom,
		StartTo:   f.StartTo,
		OrganizerID: uuid.NullUUID{
			UUID:  f.OrganizerID,
			Valid: f.OrganizerID != uuid.Nil,
		},
		Infrastructure:   f.Infrastructure,
		NeedsReservation: f.NeedsReservation,
	})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to count the events per value").
			Err()
	}
	return counts, nil
}

// ListEventOrganizers lists everyone who manages at least one event (the organizer filter). Route gate: events.read.
func (u *EventUseCase) ListEventOrganizers(ctx context.Context) ([]eventRepo.Organizer, error) {
	out, err := u.events.Organizers(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list the organizers").
			Err()
	}
	return out, nil
}
