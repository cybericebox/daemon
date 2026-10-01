package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
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
	"github.com/cybericebox/daemon/pkg/pagination"
)

var maxUUID = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
var cursorSentinelTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// CreateEvent persists a new platform event. Route gate: events.write.
func (u *EventUseCase) CreateEvent(ctx context.Context, in CreateEventInput) (EventView, error) {
	now := time.Now()
	e, err := eventModel.NewEvent(in.Tag, in.Name, in.AvailableFrom, in.ArchiveAt, in.CreatedBy, now)
	if err != nil {
		return EventView{}, err
	}
	allowed, err := u.creationInfrastructureAllowed(ctx, in.InfrastructureAllowed)
	if err != nil {
		return EventView{}, err
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
	if err = unit.Save(); err != nil {
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event").Err()
	}
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
	return toEventView(e, time.Now()), nil
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
		case "tag", "name", "status", "availableFrom", "archiveAt", "updated":
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
		rows, err := u.events.ListPage(ctx, eventRepo.PageParams{
			Search: f.Search, Status: f.Status, SortBy: sortBy, SortDir: sortDir, Now: now,
			Limit: int32(limit), Offset: int32(offset),
		})
		if err != nil {
			return EventsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list events").Err()
		}
		items := make([]EventView, 0, len(rows))
		for _, e := range rows {
			items = append(items, toEventView(e, now))
		}
		total, err := u.events.CountPage(ctx, f.Search, f.Status, now)
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
	return nil
}
