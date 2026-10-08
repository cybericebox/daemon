package event

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabGroupRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStageRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"sort"
	"time"
)

type GroupLifecycleInfrastructure interface {
	StopLabGroup(context.Context, eventLabModel.GroupTarget) error
	StartLabGroup(context.Context, eventLabModel.GroupTarget) error
	ObserveLabGroup(context.Context, string) (eventLabModel.GroupObservation, error)
}

func (u *EventUseCase) requestGroupRunningInTransaction(ctx context.Context, q IRepository, eventID, teamID uuid.UUID, name string, now time.Time) error {
	repo := eventLabGroupRepo.New(q)
	g, err := repo.Lock(ctx, teamID)
	if err != nil {
		return err
	}
	if g.EventID != eventID || g.Name != name {
		return fmt.Errorf("group admission identity changed")
	}
	pending, err := repo.Pending(ctx, teamID)
	if err != nil {
		return err
	}
	expected := g.Revision
	if !g.RequestRunning(max(pending, 1), uuid.Must(uuid.NewV7()), now) {
		return fmt.Errorf("group cannot admit a start")
	}
	ok, err := repo.Update(ctx, g, expected)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("group revision changed")
	}
	return nil
}
func (u *EventUseCase) groupAllowsDeployment(ctx context.Context, teamID uuid.UUID, now time.Time) bool {
	g, err := eventLabGroupRepo.New(u.repo).Get(ctx, teamID)
	if err != nil {
		return false
	}
	return (g.DesiredState == "Running" && g.Revision == 1) || g.AllowsChildren(now)
}
func (u *EventUseCase) ReconcileStageLabLifecycle(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	if !u.lifecycleControls {
		return nil
	}
	if u.uow == nil {
		return fmt.Errorf("stage lifecycle transaction unavailable")
	}
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return err
	}
	e, err := eventRepo.New(q).GetByID(txCtx, eventID)
	if err != nil {
		return err
	}
	stages, err := eventStageRepo.New(q).List(txCtx, eventID)
	if err != nil {
		return err
	}
	byStage := map[uuid.UUID]eventModel.Stage{}
	for _, s := range stages {
		byStage[s.ID] = s
	}
	rows, err := eventLabAllocationRepo.New(q).Labs(txCtx, eventID)
	if err != nil {
		return err
	}
	teams := map[uuid.UUID]bool{}
	for _, l := range rows {
		teams[l.TeamID] = true
	}
	teamIDs := orderedTeamIDs(teams)
	for _, teamID := range teamIDs {
		if err = eventLabRepo.New(q).LockAdmission(txCtx, teamID); err != nil {
			return err
		}
	}
	for _, row := range rows {
		l, err := eventLabRepo.New(q).Lock(txCtx, row.ID)
		if err != nil {
			return err
		}
		if l.DesiredState == "Deleted" {
			continue
		}
		set, err := eventExerciseRepo.New(q).GetByID(txCtx, eventID, l.EventExerciseID)
		if err != nil {
			return err
		}
		l.CaptureRuntimeStage(set.StageID, now)
		activeSelection, err := eventLabRetentionRepo.New(q).ActiveSelection(txCtx, l, now)
		if err != nil {
			return err
		}
		reason := ""
		base := now
		var override *int32
		if l.RuntimeStageID != nil {
			if stage, ok := byStage[*l.RuntimeStageID]; ok {
				override = stage.LabRetentionMinutes
				if !now.Before(stage.ClosesAt) && !activeSelection {
					reason = "stage"
					base = stage.ClosesAt
				}
			}
		}
		if reason == "" {
			if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && !now.Before(*finish) {
				reason = "event"
				base = *finish
			}
		}
		if reason == "" {
			continue
		}
		expected := l.Revision
		wasRunning := l.DesiredState == "Running" && l.CloseReason != "solved"
		if wasRunning {
			if err = l.Close(reason, uuid.Must(uuid.NewV7()), now); err != nil {
				return err
			}
		}
		// Terminal/already-stopped copies still receive their boundary deadline;
		// this never changes their intent, scores or restart eligibility.
		l.ApplyBoundaryRetention(base, override, now)
		if ok, err := eventLabRepo.New(q).Update(txCtx, l, expected); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("boundary retention revision changed")
		}
		if wasRunning {
			if err = u.requestLabAccessSyncInTransaction(txCtx, q, l.TeamID, now); err != nil {
				return err
			}
		}
	}
	if err = u.recomputeRetentionPinsInTransaction(txCtx, q, eventID, now); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return err
	}
	u.wakeLabLifecycle(ctx)
	return nil
}
func orderedTeamIDs(teams map[uuid.UUID]bool) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(teams))
	for id := range teams {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}
func (u *EventUseCase) reconcileGroups(ctx context.Context, now time.Time) error {
	port, ok := u.infra.(GroupLifecycleInfrastructure)
	if !ok {
		return nil
	}
	groups, err := eventLabGroupRepo.New(u.repo).List(ctx, now, u.labLifecycleBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, snapshot := range groups {
		observation, observeErr := port.ObserveLabGroup(ctx, snapshot.Name)
		if observeErr != nil {
			_ = eventLabGroupRepo.New(u.repo).Schedule(ctx, snapshot, now.Add(u.labLifecycleRetryMin))
			errs = append(errs, observeErr)
			continue
		}
		txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		func() {
			defer unit.Restore()
			if err = eventLabRepo.New(q).LockAdmission(txCtx, snapshot.TeamID); err != nil && !repositoryTools.IsObjectNotFoundError(err) {
				errs = append(errs, err)
				return
			}
			repo := eventLabGroupRepo.New(q)
			g, err := repo.Lock(txCtx, snapshot.TeamID)
			if err != nil {
				errs = append(errs, err)
				return
			}
			expected := g.Revision
			g.Observe(observation, now)
			children, err := repo.LockChildren(txCtx, g.TeamID)
			if err != nil {
				errs = append(errs, err)
				return
			}
			pending, err := repo.Pending(txCtx, g.TeamID)
			if err != nil {
				errs = append(errs, err)
				return
			}
			g.PendingStarts = pending
			authorized, err := u.groupRuntimeAuthorized(txCtx, q, g.EventID, children, now)
			if err != nil {
				errs = append(errs, err)
				return
			}
			if !authorized && pending == 0 {
				if g.RequestStop(children, uuid.Must(uuid.NewV7()), false, now) {
					deadline := now
					for _, child := range children {
						if at := child.EffectiveRetentionUntil(); at != nil && at.After(deadline) {
							deadline = *at
						}
					}
					g.RetentionUntil = &deadline
				}
			}
			if _, err = repo.Update(txCtx, g, expected); err != nil {
				errs = append(errs, err)
				return
			}
			if g.DesiredState == "Deleted" && g.RetirementState == "Deleted" {
				if _, e := q.FinalizeRetiredLabGroupPlacement(txCtx, postgres.FinalizeRetiredLabGroupPlacementParams{EventTeamID: g.TeamID, AgentUid: g.AgentUID, OperationID: g.OperationID, DesiredRevision: g.Revision}); e != nil {
					errs = append(errs, e)
					return
				}
			}
			// Hold the common team lock through bounded command submission, so an
			// admitted next-stage start cannot be followed by a stale stop dispatch.
			commandCtx, cancel := context.WithTimeout(txCtx, 30*time.Second)
			defer cancel()
			if g.DesiredState == "Stopped" && (g.ActualState != "Stopped" || g.ObservedRevision != g.Revision || g.HeldCompute() != (eventLabModel.Compute{})) {
				err = port.StopLabGroup(commandCtx, g.Target())
			} else if g.DesiredState == "Running" && g.Revision > 1 && !g.AllowsChildren(now) {
				err = port.StartLabGroup(commandCtx, g.Target())
			} else if g.DesiredState == "Deleted" && g.RetirementStopTarget != nil && g.RetirementState != "Deleted" {
				if retirement, ok := u.infra.(LabRetirementInfrastructure); ok {
					err = retirement.RetireLabGroup(commandCtx, eventLabModel.GroupRetirementRequest{StopTarget: *g.RetirementStopTarget, OperationID: g.OperationID, Revision: g.Revision})
				}
			}
			if err != nil {
				errs = append(errs, err)
			}
			g.NextAttemptAt = now.Add(u.labLifecycleRetryMin)
			_ = repo.Schedule(txCtx, g, g.NextAttemptAt)
			if saveErr := unit.Save(); saveErr != nil {
				errs = append(errs, saveErr)
			}
		}()
	}
	return errors.Join(errs...)
}
func (u *EventUseCase) groupRuntimeAuthorized(ctx context.Context, q IRepository, eventID uuid.UUID, children []eventLabModel.Lab, now time.Time) (bool, error) {
	e, err := eventRepo.New(q).GetByID(ctx, eventID)
	if repositoryTools.IsObjectNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !e.Lifecycle.RuntimeOpen(now) && e.Lifecycle.HasStarted(now) {
		return false, nil
	}
	for _, l := range children {
		if l.CloseReason == "solved" {
			continue
		}
		set, err := eventExerciseRepo.New(q).GetByID(ctx, eventID, l.EventExerciseID)
		if err != nil {
			return false, err
		}
		if set.StageID == nil && l.DesiredState == "Running" {
			return true, nil
		}
		if set.StageID != nil {
			s, err := eventStageRepo.New(q).Get(ctx, eventID, *set.StageID)
			if err != nil {
				return false, err
			}
			if s.Returnable && !now.Before(s.ClosesAt) {
				return true, nil
			}
			if l.DesiredState == "Running" && now.Before(s.ClosesAt) {
				return true, nil
			}
		}
	}
	return false, nil
}
