package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

// GetEventLifecycle returns the canonical runtime state for an event manager.
func (u *EventUseCase) GetEventLifecycle(ctx context.Context, id uuid.UUID) (EventLifecycleView, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventLifecycleView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventLifecycleView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	plan, err := u.eventInfrastructurePlan(ctx, id)
	if err != nil {
		return EventLifecycleView{}, err
	}
	return toEventLifecycleView(e, time.Now(), plan), nil
}

// UpdateEventLifecycle changes an event's runtime model. Authorization belongs
// to the event-scoped delivery gate; this use case preserves optimistic locking
// so two managers cannot silently overwrite each other's schedule.
func (u *EventUseCase) UpdateEventLifecycle(ctx context.Context, id uuid.UUID, in UpdateLifecycleInput, updatedBy uuid.UUID) (EventLifecycleView, error) {
	lifecycle, err := eventModel.NewLifecycle(in.JoinPolicy, in.PublishAt, in.StartAt, in.FinishAt, in.WithdrawAt, nil)
	if err != nil {
		return EventLifecycleView{}, err
	}
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventLifecycleView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventLifecycleView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if lifecycle.PublishAt.Before(e.AvailableFrom) ||
		(!e.ArchiveAt.IsZero() && (!lifecycle.StartAt.Before(e.ArchiveAt) ||
			(lifecycle.WithdrawAt != nil && lifecycle.WithdrawAt.After(e.ArchiveAt)))) {
		return EventLifecycleView{}, eventModel.ErrEventLifecycleInvalid.Err()
	}
	expected := e.UpdatedAt
	now := time.Now()
	// Once publication has happened, moving PublishAt into the future would
	// make the participation format appear editable again and hide an already
	// published event. Keep the original publication instant immutable.
	if e.Lifecycle.Configured && !now.Before(e.Lifecycle.PublishAt) && !lifecycle.PublishAt.Equal(e.Lifecycle.PublishAt) {
		return EventLifecycleView{}, eventModel.ErrEventLifecycleInvalid.Err()
	}
	config, err := u.configs.Get(ctx, id)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return EventLifecycleView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil {
		return EventLifecycleView{}, eventConfigModel.ErrParticipationRequired.Err()
	}
	// A future schedule may be stored while the agent is healthy; runtime paths
	// re-check later. But an update that opens runtime now must fail before the
	// lifecycle row or access-sync state is written.
	if lifecycle.RuntimeOpen(now) {
		if _, err = u.requireEventLaboratories(ctx, id); err != nil {
			return EventLifecycleView{}, err
		}
	}
	e.UpdateLifecycle(lifecycle, updatedBy, now)
	affected, err := u.events.UpdateLifecycle(ctx, e, expected)
	if err != nil {
		return EventLifecycleView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event lifecycle").Err()
	}
	if affected == 0 {
		if _, err = u.events.GetByID(ctx, id); err != nil {
			return EventLifecycleView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventLifecycleView{}, eventModel.ErrEventModified.Err()
	}
	if u.supportsLabAccessPolicy() {
		if err = u.RequestEventLabAccessSyncs(ctx, id); err != nil {
			return EventLifecycleView{}, err
		}
	}
	plan, err := u.eventInfrastructurePlan(ctx, id)
	if err != nil {
		return EventLifecycleView{}, err
	}
	return toEventLifecycleView(e, now, plan), nil
}
