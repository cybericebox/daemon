package event

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabGroupRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStageRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"sort"
	"time"
)

type LabRetirementInfrastructure interface {
	RetireLab(context.Context, eventLabModel.RetirementRequest) error
	RetireLabGroup(context.Context, eventLabModel.GroupRetirementRequest) error
}

// The event schedule and its live generation deadlines commit together. A
// sweeper holding an older due-list snapshot re-reads these rows under the same
// admission/Lab locks before it can initiate retirement.
func (u *EventUseCase) refreshWholeEventRetentionInTransaction(ctx context.Context, q IRepository, e eventModel.Event, now time.Time) error {
	rows, err := eventLabAllocationRepo.New(q).Labs(ctx, e.ID)
	if err != nil {
		return err
	}
	finish := e.Lifecycle.EffectiveFinishAt()
	teams := map[uuid.UUID]bool{}
	for _, row := range rows {
		if row.RefreshWholeEventRetention(finish, now) {
			teams[row.TeamID] = true
		}
	}
	repo := eventLabRepo.New(q)
	for _, teamID := range orderedTeamIDs(teams) {
		if err := repo.LockAdmission(ctx, teamID); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if !teams[row.TeamID] {
			continue
		}
		live, err := repo.Lock(ctx, row.ID)
		if err != nil {
			return err
		}
		expected := live.Revision
		if !live.RefreshWholeEventRetention(finish, now) {
			continue
		}
		updated, err := repo.Update(ctx, live, expected)
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("scheduled retention revision changed")
		}
	}
	return nil
}

// SelectRetainedLabsForStage is an internal coordinator port. No participant or
// operator API accepts arbitrary Lab IDs. The caller supplies exact authorized
// runtime membership, never an exercise/name similarity heuristic.
func (u *EventUseCase) SelectRetainedLabsForStage(ctx context.Context, eventID, stageID uuid.UUID, labIDs []uuid.UUID) error {
	now := time.Now().UTC()
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return err
	}
	stage, err := eventStageRepo.New(q).Get(txCtx, eventID, stageID)
	if err != nil {
		return err
	}
	if !now.Before(stage.ClosesAt) {
		return fmt.Errorf("retained runtime selection is outside its stage")
	}
	repo := eventLabRetentionRepo.New(q)
	teams := map[uuid.UUID]bool{}
	for _, id := range labIDs {
		l, e := eventLabRepo.New(q).Get(txCtx, id)
		if e != nil {
			return e
		}
		teams[l.TeamID] = true
	}
	for _, teamID := range orderedTeamIDs(teams) {
		if err = eventLabRepo.New(q).LockAdmission(txCtx, teamID); err != nil {
			return err
		}
	}
	sort.Slice(labIDs, func(i, j int) bool { return labIDs[i].String() < labIDs[j].String() })
	for _, id := range labIDs {
		l, err := eventLabRepo.New(q).Lock(txCtx, id)
		if err != nil {
			return err
		}
		if l.EventID != eventID || l.CloseReason == "solved" || l.DesiredState == "Deleted" {
			return fmt.Errorf("retained runtime selection is not eligible")
		}
		if deadline := l.EffectiveRetentionUntil(); l.DesiredState == "Stopped" && deadline != nil && !now.Before(*deadline) {
			return fmt.Errorf("retained runtime selection has expired")
		}
		if err = repo.Select(txCtx, eventID, stageID, id, now); err != nil {
			return err
		}
	}
	if err = u.recomputeRetentionPinsInTransaction(txCtx, q, eventID, now); err != nil {
		return err
	}
	return unit.Save()
}
func (u *EventUseCase) recomputeRetentionPinsInTransaction(ctx context.Context, q IRepository, eventID uuid.UUID, now time.Time) error {
	if !u.lifecycleControls {
		return nil
	}
	repo := eventLabRetentionRepo.New(q)
	if err := repo.Recompute(ctx, eventID, 0, now); err != nil {
		return err
	}
	e, err := eventRepo.New(q).GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	stages, err := eventStageRepo.New(q).List(ctx, eventID)
	if err != nil {
		return err
	}
	planning := NewEventUseCase(Dependencies{Repo: q, Infra: u.infra, Resources: u.resources})
	planning.allocationAccounting = true
	planning.prewarmLead = u.prewarmLead
	inputs, err := planning.resourcePlanInputs(ctx, eventID)
	if err != nil {
		return err
	}
	links, err := eventExerciseRepo.New(q).List(ctx, eventID)
	if err != nil {
		return err
	}
	stageOf := map[uuid.UUID]*uuid.UUID{}
	for _, link := range activeAttachments(links) {
		stageOf[link.ID] = link.StageID
	}
	sets := make([]eventModel.SetLoad, 0, len(inputs.tasks)+len(stages))
	for _, task := range inputs.tasks {
		sets = append(sets, eventModel.SetLoad{ExerciseID: task.EventExerciseID, StageID: stageOf[task.EventExerciseID], Pods: inputs.teams * task.labDevices})
	}
	// A future group wake has its two own service pods before restore/wiring.
	for i, stage := range stages {
		if i == 0 {
			continue
		}
		id := uuid.NewV5(stage.ID, "group-preparation")
		sid := stage.ID
		sets = append(sets, eventModel.SetLoad{ExerciseID: id, StageID: &sid, Pods: inputs.teams * 2})
	}
	memberships, err := q.ListEventStageRuntimeMemberships(ctx, eventID)
	if err != nil {
		return err
	}
	for _, m := range memberships {
		if assigned := stageOf[m.EventExerciseID]; assigned != nil && *assigned == m.StageID {
			continue
		}
		pods := 0
		if m.DefinitionVersionID.Valid {
			v, err := exerciseRepo.New(q).GetVersion(ctx, m.DefinitionVersionID.UUID)
			if err != nil {
				return err
			}
			if m.VariantIndex < 0 || int(m.VariantIndex) >= len(v.Variants) {
				return fmt.Errorf("selected pinned variant is absent")
			}
			pods = len(u.resourcePolicy().Devices(v.Variants[m.VariantIndex].Topology))
		} else {
			return fmt.Errorf("selected pinned definition is unknown")
		}
		sid := m.StageID
		sets = append(sets, eventModel.SetLoad{ExerciseID: m.LabID, StageID: &sid, Pods: pods})
	}
	plan := eventModel.PlanStageDeploy(e.Lifecycle.StartAt, stages, sets, inputs.teams*2, u.leadFunc())
	for _, stage := range stages {
		var due *time.Time
		for _, set := range sets {
			if set.StageID != nil && *set.StageID == stage.ID {
				at := plan.DueAt[set.ExerciseID]
				if due == nil || at.Before(*due) {
					copy := at
					due = &copy
				}
			}
		}
		if due != nil {
			if err = repo.SetDue(ctx, stage.ID, *due); err != nil {
				return err
			}
		}
	}
	if err = q.RecomputeEventGroupRetentionPins(ctx, eventID); err != nil {
		return err
	}
	if err = q.RefreshEventLabProtectedUntil(ctx, eventID); err != nil {
		return err
	}
	return q.RefreshEventGroupProtectedUntil(ctx, eventID)
}

func (u *EventUseCase) ReconcileLabRetention(ctx context.Context, now time.Time) error {
	if !u.lifecycleControls || u.uow == nil {
		return nil
	}
	repo := eventLabRetentionRepo.New(u.repo)
	orphans, err := repo.Orphans(ctx, u.labLifecycleBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, snapshot := range orphans {
		txCtx, q, unit, e := u.uow.UnitOfWork(ctx)
		if e != nil {
			errs = append(errs, e)
			continue
		}
		func() {
			defer unit.Restore()
			l, e := eventLabRepo.New(q).Lock(txCtx, snapshot.ID)
			if e != nil {
				errs = append(errs, e)
				return
			}
			if l.DesiredState != "Running" {
				return
			}
			expected := l.Revision
			if e = l.Close("event", uuid.Must(uuid.NewV7()), now); e != nil {
				errs = append(errs, e)
				return
			}
			l.SetRetentionDeadline(now.Add(time.Duration(l.RetentionMinutes)*time.Minute), now)
			if _, e = eventLabRepo.New(q).Update(txCtx, l, expected); e != nil {
				errs = append(errs, e)
				return
			}
			if e = unit.Save(); e != nil {
				errs = append(errs, e)
			}
		}()
	}
	due, err := repo.DueLabs(ctx, now, u.labLifecycleBatch)
	if err != nil {
		return err
	}
	for _, snapshot := range due {
		txCtx, q, unit, e := u.uow.UnitOfWork(ctx)
		if e != nil {
			errs = append(errs, e)
			continue
		}
		func() {
			defer unit.Restore()
			if e = eventLabRepo.New(q).LockAdmission(txCtx, snapshot.TeamID); e != nil && !repositoryTools.IsObjectNotFoundError(e) {
				errs = append(errs, e)
				return
			}
			l, e := eventLabRepo.New(q).Lock(txCtx, snapshot.ID)
			if e != nil {
				errs = append(errs, e)
				return
			}
			pinned, e := eventLabRetentionRepo.New(q).Pinned(txCtx, l, now)
			if e != nil {
				errs = append(errs, e)
				return
			}
			if pinned {
				return
			}
			expected := l.Revision
			if !l.RequestRetirement(uuid.Must(uuid.NewV7()), now) {
				return
			}
			if e = eventLabRetentionRepo.New(q).Archive(txCtx, l.ID); e != nil {
				errs = append(errs, e)
				return
			}
			if _, e = eventLabRepo.New(q).Update(txCtx, l, expected); e != nil {
				errs = append(errs, e)
				return
			}
			if e = unit.Save(); e != nil {
				errs = append(errs, e)
			}
		}()
	}
	retiring, err := repo.Retiring(ctx, u.labLifecycleBatch)
	if err != nil {
		return err
	}
	port, retirementSupported := u.infra.(LabRetirementInfrastructure)
	observer, observationSupported := u.infra.(LabLifecycleInfrastructure)
	if retirementSupported && observationSupported {
		for _, l := range retiring {
			if l.RetirementStopTarget == nil {
				continue
			}
			commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			e := port.RetireLab(commandCtx, eventLabModel.RetirementRequest{StopTarget: *l.RetirementStopTarget, OperationID: l.OperationID, Revision: l.Revision})
			if e == nil {
				o, observeErr := observer.ObserveLab(commandCtx, l.Ref)
				if observeErr == nil {
					_, e = u.labs.RecordObservation(commandCtx, l.ID, o)
				} else {
					e = observeErr
				}
			}
			cancel()
			if e != nil {
				errs = append(errs, e)
			}
		}
	}
	groups, err := repo.DueGroups(ctx, now, u.labLifecycleBatch)
	if err != nil {
		return err
	}
	for _, snapshot := range groups {
		txCtx, q, unit, e := u.uow.UnitOfWork(ctx)
		if e != nil {
			errs = append(errs, e)
			continue
		}
		func() {
			defer unit.Restore()
			if e = eventLabRepo.New(q).LockAdmission(txCtx, snapshot.TeamID); e != nil && !repositoryTools.IsObjectNotFoundError(e) {
				errs = append(errs, e)
				return
			}
			g, e := eventLabGroupRepo.New(q).Lock(txCtx, snapshot.TeamID)
			if e != nil {
				errs = append(errs, e)
				return
			}
			expected := g.Revision
			if !g.RequestRetirement(uuid.Must(uuid.NewV7()), now) {
				return
			}
			if _, e = eventLabGroupRepo.New(q).Update(txCtx, g, expected); e != nil {
				errs = append(errs, e)
				return
			}
			if e = unit.Save(); e != nil {
				errs = append(errs, e)
			}
		}()
	}
	return errors.Join(errs...)
}

// Due runtime selection is explicit system work; it never restarts solved IDs
// or a manually stopped Lab outside its exact persisted stage membership.
func (u *EventUseCase) prepareRetainedStageSelections(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	selections, err := eventLabRetentionRepo.New(u.repo).DueSelections(ctx, eventID, now)
	if err != nil {
		return err
	}
	for _, snapshot := range selections {
		txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
		if err != nil {
			return err
		}
		err = func() error {
			defer unit.Restore()
			if _, err := q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
				return err
			}
			if err := q.LockResourceCalendar(txCtx); err != nil {
				return err
			}
			if err := eventLabRepo.New(q).LockAdmission(txCtx, snapshot.Lab.TeamID); err != nil {
				return err
			}
			l, err := eventLabRepo.New(q).Lock(txCtx, snapshot.Lab.ID)
			if err != nil {
				return err
			}
			selected := eventLabRetentionRepo.New(q)
			if err = selected.LockSelection(txCtx, eventID, snapshot, now); repositoryTools.IsObjectNotFoundError(err) {
				return nil
			} else if err != nil {
				return err
			}
			e, err := eventRepo.New(q).GetByID(txCtx, eventID)
			if err != nil {
				return err
			}
			if !e.InfrastructureAllowed {
				return nil
			}
			if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && !now.Before(*finish) {
				return nil
			}
			stage, err := eventStageRepo.New(q).Get(txCtx, eventID, snapshot.StageID)
			if err != nil {
				return err
			}
			if !now.Before(stage.ClosesAt) || l.Generation != snapshot.Lab.Generation || l.CloseReason == "solved" || l.DesiredState == "Deleted" {
				return nil
			}
			if l.Revision != snapshot.Revision && !(l.DesiredState == "Stopped" && l.CloseReason == "stage" && l.Revision == snapshot.Revision+1) {
				return nil
			}
			if err = requireTeamAdmitted(txCtx, eventTeamRepo.New(q), eventID, l.TeamID); err != nil {
				return err
			}
			caps, known := u.labCaps(txCtx, l.Ref.Group)
			if !known || !caps.ConfirmedRuntime || !caps.RetainedRestart {
				return nil
			}
			expected := l.Revision
			if l.DesiredState == "Stopped" {
				if err = l.Start(uuid.Must(uuid.NewV7()), now); err != nil {
					return err
				}
				if !l.Admit(l.Allocation.ConfiguredRequests, l.Allocation.SnapshotQuotaBytes, now) && !l.AdmitKnown(l.Allocation.ConfiguredRequests, l.Allocation.SnapshotQuotaBytes, l.DefinitionHash, l.Generation, now) {
					return fmt.Errorf("retained stage request is unknown")
				}
				cfg, err := eventConfigRepo.New(q).Get(txCtx, eventID)
				if err != nil {
					return err
				}
				if err = u.reserveLabCandidateInTransaction(txCtx, q, l, cfg, now); err != nil {
					return err
				}
				if err = u.requestGroupRunningInTransaction(txCtx, q, eventID, l.TeamID, l.Ref.Group, now); err != nil {
					return err
				}
			} else if l.DesiredState != "Running" {
				return nil
			}
			if !l.AuthorizeRuntimeStage(snapshot.StageID, now) {
				return nil
			}
			updated, err := eventLabRepo.New(q).Update(txCtx, l, expected)
			if err != nil {
				return err
			}
			if !updated {
				return fmt.Errorf("stage preparation revision changed")
			}
			consumed, err := selected.Consume(txCtx, snapshot, l.Revision, now)
			if err != nil {
				return err
			}
			if !consumed {
				return fmt.Errorf("stage preparation authorization changed")
			}
			if err = u.requestLabAccessSyncInTransaction(txCtx, q, l.TeamID, now); err != nil {
				return err
			}
			return unit.Save()
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
