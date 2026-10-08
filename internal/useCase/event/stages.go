package event

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStageRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
)

// EventStageView is a stage with its computed state and its place in the order. First and Last say which
// boundary of the event window the stage is anchored to (the first opens with the event, the last closes with it).
type EventStageView struct {
	LabRetentionMinutes *int32
	ID                  uuid.UUID
	Name                string
	OpensAt             time.Time
	ClosesAt            time.Time
	Returnable          bool
	State               eventModel.StageState
	First               bool
	Last                bool
	// DeployLeadMinutes is the lead the platform computes for the labs that open with this stage (the first stage:
	// with the event start): its deploy starts that long before OpensAt. 0 when the workload cannot be read.
	DeployLeadMinutes int
}

// CreateStageInput is a stage creation request. The boundary times of the first and last stage are the event's own,
// whatever is sent.
type CreateStageInput struct {
	LabRetentionMinutes *int32
	Name                string
	OpensAt             time.Time
	ClosesAt            time.Time
	Returnable          bool
}

// UpdateStageInput is a partial update; nil fields keep their value.
type UpdateStageInput struct {
	LabRetentionMinutes OptionalLimit
	Name                *string
	OpensAt             *time.Time
	ClosesAt            *time.Time
	Returnable          *bool
	// CloseNow is «Закрити зараз»: an open stage ends now.
	CloseNow bool
}

func toStageViews(stages []eventModel.Stage, now time.Time) []EventStageView {
	out := make([]EventStageView, 0, len(stages))
	for i, stage := range stages {
		out = append(out, EventStageView{LabRetentionMinutes: stage.LabRetentionMinutes, ID: stage.ID, Name: stage.Name, OpensAt: stage.OpensAt, ClosesAt: stage.ClosesAt,
			Returnable: stage.Returnable, State: stage.State(now), First: i == 0, Last: i == len(stages)-1})
	}
	return out
}

// classifyStageWriteError maps the database guards to the stage errors.
func classifyStageWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == pgerrcode.ExclusionViolation && pgErr.ConstraintName == "event_stages_no_overlap":
			return eventModel.ErrEventStageOverlap.Err()
		case pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == "event_stages_event_name_idx":
			return eventModel.ErrEventStageNameExists.Err()
		case pgErr.Code == pgerrcode.CheckViolation:
			return eventModel.ErrEventStageInvalid.Err()
		case pgErr.Code == pgerrcode.ForeignKeyViolation:
			return eventModel.ErrEventStageNotDeletable.Err()
		}
	}
	return model.ErrPlatform.WithError(err).WithMessage("Failed to " + action + " event stage").Err()
}

func (u *EventUseCase) requestStageSyncs(ctx context.Context, eventID uuid.UUID) error {
	u.invalidateLeadPlan(eventID)
	if !u.supportsLabAccessPolicy() {
		return nil
	}
	return u.RequestEventLabAccessSyncs(ctx, eventID)
}

// ListEventStages returns the event's stages in time order with their computed state.
func (u *EventUseCase) ListEventStages(ctx context.Context, eventID uuid.UUID) ([]EventStageView, error) {
	if _, err := u.events.GetByID(ctx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	stages, err := u.stages.List(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	now := time.Now()
	views := toStageViews(stages, now)
	u.fillStageLeads(ctx, eventID, stages, views, now)
	return views, nil
}

// fillStageLeads sets the computed deploy lead of each stage (a failed read leaves it 0: the lead is informational).
func (u *EventUseCase) fillStageLeads(ctx context.Context, eventID uuid.UUID, stages []eventModel.Stage, views []EventStageView, now time.Time) {
	if len(stages) == 0 {
		return
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return
	}
	sets, extra, err := u.setLoads(ctx, e, now)
	if err != nil {
		return
	}
	lead := u.leadFunc()
	pods := make(map[uuid.UUID]int, len(stages))
	initial := extra
	for _, set := range sets {
		if set.StageID == nil || *set.StageID == stages[0].ID {
			initial += set.Pods
			continue
		}
		pods[*set.StageID] += set.Pods
	}
	for i := range views {
		n := pods[views[i].ID]
		if i == 0 {
			n = initial
		}
		views[i].DeployLeadMinutes = int((lead(n) + time.Minute - 1) / time.Minute)
	}
}

func (u *EventUseCase) stageEvent(ctx context.Context, repo eventRepo.Queries, eventID uuid.UUID) (eventModel.Event, error) {
	e, err := eventRepo.New(repo).GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.Event{}, eventModel.ErrEventNotFound.Err()
		}
		return eventModel.Event{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !e.Lifecycle.Configured || e.Lifecycle.FinishAt == nil {
		return eventModel.Event{}, eventModel.ErrEventStageNeedsFinish.Err()
	}
	return e, nil
}

// CreateEventStage adds a stage (see eventModel.PlanCreateStage for where it lands).
func (u *EventUseCase) CreateEventStage(ctx context.Context, eventID uuid.UUID, in CreateStageInput) (EventStageView, error) {
	if u.uow == nil {
		return EventStageView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	now := time.Now()
	candidate, err := eventModel.NewStage(eventID, in.Name, in.OpensAt, in.ClosesAt, in.Returnable, now)
	if err != nil {
		return EventStageView{}, err
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventStageView{}, err
	}
	defer unit.Restore()
	if u.lifecycleControls {
		if _, err = txRepo.LockEventForLabSourceChange(txCtx, eventID); err != nil {
			return EventStageView{}, err
		}
	}
	e, err := u.stageEvent(txCtx, txRepo, eventID)
	if err != nil {
		return EventStageView{}, err
	}
	repo := eventStageRepo.New(txRepo)
	existing, err := repo.List(txCtx, eventID)
	if err != nil {
		return EventStageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	if len(existing) > 0 && in.OpensAt.IsZero() {
		return EventStageView{}, eventModel.ErrEventStageInvalid.Err()
	}
	plan, err := eventModel.PlanCreateStage(existing, candidate, e.Lifecycle.StartAt, e.Lifecycle.FinishAt, now)
	if err != nil {
		return EventStageView{}, err
	}
	if plan.Trimmed != nil {
		if _, err = repo.Update(txCtx, *plan.Trimmed); err != nil {
			return EventStageView{}, classifyStageWriteError(err, "update")
		}
	}
	if err = plan.Created.SetLabRetentionMinutes(in.LabRetentionMinutes, now); err != nil {
		return EventStageView{}, err
	}
	if _, err = repo.Create(txCtx, plan.Created); err != nil {
		return EventStageView{}, classifyStageWriteError(err, "create")
	}
	if u.lifecycleControls {
		if err = u.recomputeRetentionPinsInTransaction(txCtx, txRepo, eventID, now); err != nil {
			return EventStageView{}, err
		}
	}
	if err = unit.Save(); err != nil {
		return EventStageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event stage").Err()
	}
	if err = u.requestStageSyncs(ctx, eventID); err != nil {
		return EventStageView{}, err
	}
	return u.stageView(ctx, eventID, plan.Created.ID, now)
}

func (u *EventUseCase) stageView(ctx context.Context, eventID, stageID uuid.UUID, now time.Time) (EventStageView, error) {
	stages, err := u.stages.List(ctx, eventID)
	if err != nil {
		return EventStageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	for _, view := range toStageViews(stages, now) {
		if view.ID == stageID {
			return view, nil
		}
	}
	return EventStageView{}, eventModel.ErrEventStageNotFound.Err()
}

// UpdateEventStage edits a stage by what its state allows (eventModel.Stage.Apply).
func (u *EventUseCase) UpdateEventStage(ctx context.Context, eventID, stageID uuid.UUID, in UpdateStageInput) (EventStageView, error) {
	if u.uow == nil {
		return EventStageView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	now := time.Now()
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventStageView{}, err
	}
	defer unit.Restore()
	if u.lifecycleControls {
		if _, err = txRepo.LockEventForLabSourceChange(txCtx, eventID); err != nil {
			return EventStageView{}, err
		}
	}
	e, err := u.stageEvent(txCtx, txRepo, eventID)
	if err != nil {
		return EventStageView{}, err
	}
	repo := eventStageRepo.New(txRepo)
	stages, err := repo.List(txCtx, eventID)
	if err != nil {
		return EventStageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	index := -1
	for i, stage := range stages {
		if stage.ID == stageID {
			index = i
		}
	}
	if index < 0 {
		return EventStageView{}, eventModel.ErrEventStageNotFound.Err()
	}
	edited, err := stages[index].Apply(eventModel.StageEdit{Name: in.Name, OpensAt: in.OpensAt, ClosesAt: in.ClosesAt, Returnable: in.Returnable, CloseNow: in.CloseNow},
		now, index == 0, index == len(stages)-1, e.Lifecycle.StartAt, e.Lifecycle.FinishAt)
	if err != nil {
		return EventStageView{}, err
	}
	next := append([]eventModel.Stage(nil), stages...)
	next[index] = edited
	if err = eventModel.ValidateStages(next, e.Lifecycle.StartAt, e.Lifecycle.FinishAt, now); err != nil {
		return EventStageView{}, err
	}
	if in.LabRetentionMinutes.Set {
		if err = edited.SetLabRetentionMinutes(in.LabRetentionMinutes.Value, now); err != nil {
			return EventStageView{}, err
		}
	}
	if _, err = repo.Update(txCtx, edited); err != nil {
		return EventStageView{}, classifyStageWriteError(err, "update")
	}
	if u.lifecycleControls {
		if err = u.recomputeRetentionPinsInTransaction(txCtx, txRepo, eventID, now); err != nil {
			return EventStageView{}, err
		}
	}
	if err = unit.Save(); err != nil {
		return EventStageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event stage").Err()
	}
	if err = u.requestStageSyncs(ctx, eventID); err != nil {
		return EventStageView{}, err
	}
	return u.stageView(ctx, eventID, stageID, now)
}

// DeleteEventStage removes an upcoming stage without exercises; the stage that becomes first or last is anchored to the
// event start or finish.
func (u *EventUseCase) DeleteEventStage(ctx context.Context, eventID, stageID uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	now := time.Now()
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if u.lifecycleControls {
		if _, err = txRepo.LockEventForLabSourceChange(txCtx, eventID); err != nil {
			return err
		}
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	repo := eventStageRepo.New(txRepo)
	stage, err := repo.Get(txCtx, eventID, stageID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventStageNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event stage").Err()
	}
	sets, err := repo.CountSets(txCtx, stageID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count stage sets").Err()
	}
	if err = eventModel.CheckStageDelete(stage, sets, now); err != nil {
		return err
	}
	if u.lifecycleControls {
		if err = txRepo.RemoveEventStageRuntimeSelections(txCtx, stageID); err != nil {
			return err
		}
	}
	if _, err = repo.Delete(txCtx, eventID, stageID); err != nil {
		return classifyStageWriteError(err, "delete")
	}
	remaining, err := repo.List(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	for _, changed := range eventModel.AnchorAfterDelete(remaining, e.Lifecycle.StartAt, e.Lifecycle.FinishAt, now) {
		if _, err = repo.Update(txCtx, changed); err != nil {
			return classifyStageWriteError(err, "update")
		}
	}
	if u.lifecycleControls {
		if err = u.recomputeRetentionPinsInTransaction(txCtx, txRepo, eventID, now); err != nil {
			return err
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event stage").Err()
	}
	return u.requestStageSyncs(ctx, eventID)
}

// SetEventExerciseStage attaches an exercise set to a stage, or to the whole event (nil). Before a stage opens
// everything is free; once it has opened nothing leaves it, and a closed stage accepts nothing new.
func (u *EventUseCase) SetEventExerciseStage(ctx context.Context, eventID, eventExerciseID uuid.UUID, stageID *uuid.UUID) (EventExerciseView, error) {
	if u.uow == nil {
		return EventExerciseView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	now := time.Now()
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventExerciseView{}, err
	}
	defer unit.Restore()
	if u.lifecycleControls {
		if _, err = txRepo.LockEventForLabSourceChange(txCtx, eventID); err != nil {
			return EventExerciseView{}, err
		}
	}
	sets := eventExerciseRepo.New(txRepo)
	link, err := sets.GetByID(txCtx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventExerciseView{}, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if err = link.EnsureActive(); err != nil {
		return EventExerciseView{}, err
	}
	if sameStageID(link.StageID, stageID) {
		return toEventExerciseView(link), nil
	}
	repo := eventStageRepo.New(txRepo)
	var from, to *eventModel.Stage
	if link.StageID != nil {
		stage, getErr := repo.Get(txCtx, eventID, *link.StageID)
		if getErr != nil {
			return EventExerciseView{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event stage").Err()
		}
		from = &stage
	}
	if stageID != nil {
		stage, getErr := repo.Get(txCtx, eventID, *stageID)
		if getErr != nil {
			if repositoryTools.IsObjectNotFoundError(getErr) {
				return EventExerciseView{}, eventModel.ErrEventStageNotFound.Err()
			}
			return EventExerciseView{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event stage").Err()
		}
		to = &stage
	}
	if err = eventModel.CheckSetMove(from, to, now); err != nil {
		return EventExerciseView{}, err
	}
	moved, err := sets.SetStage(txCtx, eventID, eventExerciseID, stageID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventExerciseView{}, eventExerciseModel.ErrEventExerciseNotActive.Err()
		}
		return EventExerciseView{}, classifyStageWriteError(err, "move set to")
	}
	if u.lifecycleControls {
		if stageID != nil {
			all, pinErr := eventLabAllocationRepo.New(txRepo).Labs(txCtx, eventID)
			if pinErr != nil {
				return EventExerciseView{}, pinErr
			}
			for _, l := range all {
				if l.EventExerciseID == eventExerciseID && l.CloseReason != "solved" && l.DesiredState != "Deleted" {
					if pinErr = eventLabRetentionRepo.New(txRepo).Select(txCtx, eventID, *stageID, l.ID, now); pinErr != nil {
						return EventExerciseView{}, pinErr
					}
				}
			}
		}
		if err = u.recomputeRetentionPinsInTransaction(txCtx, txRepo, eventID, now); err != nil {
			return EventExerciseView{}, err
		}
	}
	if err = unit.Save(); err != nil {
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to set the stage of the event exercise").Err()
	}
	if err = u.requestStageSyncs(ctx, eventID); err != nil {
		return EventExerciseView{}, err
	}
	return toEventExerciseView(moved), nil
}

func sameStageID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// stageOfSet loads the stage a set belongs to, nil for a whole-event set.
func (u *EventUseCase) stageOfSet(ctx context.Context, repo *eventStageRepo.Repository, link eventExerciseModel.EventExercise) (*eventModel.Stage, error) {
	if link.StageID == nil {
		return nil, nil
	}
	stage, err := repo.Get(ctx, link.EventID, *link.StageID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event stage").Err()
	}
	return &stage, nil
}

// invalidateLeadPlan drops the cached workload of an event, so the next stand pass sees the new stage layout.
func (u *EventUseCase) invalidateLeadPlan(eventID uuid.UUID) {
	u.placement.mu.Lock()
	delete(u.placement.plans, eventID)
	u.placement.mu.Unlock()
}
