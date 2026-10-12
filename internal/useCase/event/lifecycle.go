package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStageRepo"
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
	if e.Lifecycle.ManualFinishAt != nil {
		// Editing scheduled dates must retain an actual finish already recorded by a manager.
		lifecycle, err = eventModel.NewLifecycle(in.JoinPolicy, in.PublishAt, in.StartAt, in.FinishAt, in.WithdrawAt, e.Lifecycle.ManualFinishAt)
		if err != nil {
			return EventLifecycleView{}, err
		}
	}
	if lifecycle.PublishAt.Before(e.AvailableFrom) ||
		(!e.ArchiveAt.IsZero() && (!lifecycle.StartAt.Before(e.ArchiveAt) ||
			(lifecycle.FinishAt != nil && lifecycle.FinishAt.After(e.ArchiveAt)) ||
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
	affected, err := u.saveLifecycleWithStages(ctx, e, expected, now)
	if err != nil {
		return EventLifecycleView{}, err
	}
	if affected == 0 {
		if _, err = u.events.GetByID(ctx, id); err != nil {
			return EventLifecycleView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventLifecycleView{}, eventModel.ErrEventModified.Err()
	}
	u.invalidateLeadPlan(id)
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

// saveLifecycleWithStages writes the event schedule after validating the stages within its new bounds.
// Only explicitly bound, future stage dates follow the event schedule.
func (u *EventUseCase) saveLifecycleWithStages(ctx context.Context, e eventModel.Event, expected, now time.Time) (int64, error) {
	stages, err := u.stages.List(ctx, e.ID)
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	if len(stages) == 0 && !u.lifecycleControls {
		affected, updateErr := u.events.UpdateLifecycle(ctx, e, expected)
		if updateErr != nil {
			return 0, model.ErrPlatform.WithError(updateErr).WithMessage("Failed to update event lifecycle").Err()
		}
		return affected, nil
	}
	if u.uow == nil {
		return 0, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	changed, err := eventModel.PlanStageLifecycle(stages, e.Lifecycle.StartAt, e.Lifecycle.FinishAt, e.ArchiveAt, now)
	if err != nil {
		return 0, err
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return 0, err
	}
	defer unit.Restore()
	stageRepo := eventStageRepo.New(txRepo)
	if u.lifecycleControls {
		if _, err = txRepo.LockEventForLabSourceChange(txCtx, e.ID); err != nil {
			return 0, err
		}
		// Source/selection changes share this event lock. Re-read their stage
		// windows before updating the schedule and its dependent retention.
		stages, err = stageRepo.List(txCtx, e.ID)
		if err != nil {
			return 0, err
		}
		changed, err = eventModel.PlanStageLifecycle(stages, e.Lifecycle.StartAt, e.Lifecycle.FinishAt, e.ArchiveAt, now)
		if err != nil {
			return 0, err
		}
	}
	for _, stage := range changed {
		if _, err = stageRepo.Update(txCtx, stage); err != nil {
			return 0, classifyStageWriteError(err, "update")
		}
	}

	affected, err := u.eventsIn(txRepo).UpdateLifecycle(txCtx, e, expected)
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to update event lifecycle").Err()
	}
	if affected == 0 {
		return 0, nil
	}
	if u.lifecycleControls {
		if err = u.refreshWholeEventRetentionInTransaction(txCtx, txRepo, e, now); err != nil {
			return 0, err
		}
		if err = u.recomputeRetentionPinsInTransaction(txCtx, txRepo, e.ID, now); err != nil {
			return 0, err
		}
	}
	if err = unit.Save(); err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to update event lifecycle").Err()
	}
	return affected, nil
}

func (u *EventUseCase) eventsIn(repo IRepository) *eventRepo.Repository { return eventRepo.New(repo) }
