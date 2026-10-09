package event

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labAccessSyncRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"time"
)

// LabLifecycleInfrastructure accepts intent separately from certified runtime
// observations. A nil command error is never an allocation acknowledgement.
type LabLifecycleInfrastructure interface {
	StopLab(context.Context, eventLabModel.StopRequest) error
	StartLab(context.Context, eventLabModel.Target) error
	ObserveLab(context.Context, eventLabModel.Ref) (eventLabModel.Observation, error)
}

// Every solve mutation takes the Lab before any question or attempt row. A
// static question has no Lab. Missing shared materialization remains unclosed.
func lockChallengeLabInTransaction(ctx context.Context, repo IRepository, teamID, challengeID uuid.UUID) (*eventLabModel.Lab, error) {
	labs := eventLabRepo.New(repo)
	if err := labs.LockAdmission(ctx, teamID); err != nil {
		return nil, err
	}
	lab, err := labs.GetForChallenge(ctx, teamID, challengeID)
	if repositoryTools.IsObjectNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lab, err = labs.Lock(ctx, lab.ID)
	if err != nil {
		return nil, err
	}
	return &lab, nil
}
func completeLabInTransaction(ctx context.Context, repo IRepository, teamID, challengeID uuid.UUID, now time.Time) (*ParticipantLabView, error) {
	lab, err := lockChallengeLabInTransaction(ctx, repo, teamID, challengeID)
	if err != nil || lab == nil {
		return nil, err
	}
	labs := eventLabRepo.New(repo)
	complete, err := labs.Complete(ctx, lab.ID)
	if err != nil {
		return nil, err
	}
	if complete && lab.CloseReason != "solved" {
		// Identity is acquired during preparation, before publication, never by an
		// agent call while the answer transaction owns row locks.
		if lab.AgentUID == "" || lab.AgentGeneration <= 0 {
			return nil, fmt.Errorf("complete lab has no initial agent identity")
		}
		revision := lab.Revision
		if err = lab.Close("solved", uuid.Must(uuid.NewV7()), now); err != nil {
			return nil, err
		}
		updated, err := labs.Update(ctx, *lab, revision)
		if err != nil {
			return nil, err
		}
		if !updated {
			return nil, fmt.Errorf("lab lifecycle revision changed")
		}
		// Durable denial intent is atomic even when an old agent lacks the ACL port.
		if err = labAccessSyncRepo.New(repo).Request(ctx, teamID, now); err != nil {
			return nil, err
		}
	}
	view := participantLabView(*lab)
	return &view, nil
}
func (u *EventUseCase) SetLabLifecycleWake(wake func(context.Context) error) {
	u.labLifecycleWake = wake
}
func (u *EventUseCase) wakeLabLifecycle(ctx context.Context) {
	if u.labLifecycleWake != nil {
		_ = u.labLifecycleWake(context.WithoutCancel(ctx))
	}
}

// A bounded periodic pass discovers work from persisted intent after any lost
// wake or process restart. Acceptance leaves that intent dirty until certified.
func (u *EventUseCase) ReconcilePendingLabLifecycles(ctx context.Context) error {
	if !u.laboratoriesConfigured() {
		return nil
	}
	now := time.Now()
	var labs []eventLabModel.Lab
	var err error
	if u.lifecycleControls {
		labs, err = u.labs.ListDirty(ctx, now, u.labLifecycleBatch)
	} else {
		labs, err = u.labs.PendingStopped(ctx, now, u.labLifecycleBatch)
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, lab := range labs {
		err = nil
		if u.lifecycleControls && lab.DesiredState == "Running" {
			err = u.reconcileRunningLab(ctx, lab, now)
		} else if lab.DesiredState == "Stopped" {
			err = u.reconcileStoppedLab(ctx, lab)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("lab %s: %w", lab.ID, err))
		}
		// Persist delay for both failures and accepted but still unacknowledged work.
		delay := u.labLifecycleRetryMin
		if err != nil {
			previous := lab.NextAttemptAt.Sub(lab.UpdatedAt)
			if previous >= delay {
				delay = min(previous*2, u.labLifecycleRetryMax)
			}
		}
		if retryErr := u.labs.ScheduleLifecycleRetry(ctx, lab, now, now.Add(delay)); retryErr != nil {
			errs = append(errs, retryErr)
		}
	}
	if u.lifecycleControls {
		if e := u.reconcileGroups(ctx, now); e != nil {
			errs = append(errs, e)
		}
		if e := u.ReconcileLabRetention(ctx, now); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
func (u *EventUseCase) reconcileStoppedLab(ctx context.Context, lab eventLabModel.Lab) error {
	port, ok := u.infra.(LabLifecycleInfrastructure)
	if !ok {
		return infraUnavailable()
	}
	if lab.AgentUID == "" {
		birth, err := port.ObserveLab(ctx, lab.Ref)
		if err != nil {
			return err
		}
		adopted, err := u.labs.RecordBirthIdentity(ctx, lab.ID, birth, time.Now().UTC())
		if err != nil {
			return err
		}
		if !adopted {
			return fmt.Errorf("original lab birth identity unavailable")
		}
		current, err := u.labs.Get(ctx, lab.ID)
		if err != nil {
			return err
		}
		if current.OperationID != lab.OperationID || current.Revision != lab.Revision || current.DesiredState != "Stopped" {
			return nil
		}
		lab = current
	}
	if lab.AgentGeneration <= 0 {
		return fmt.Errorf("lab birth generation unavailable")
	}
	if u.infrastructureCapability != nil {
		if err := u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
			return err
		}
	}
	observation, err := port.ObserveLab(ctx, lab.Ref)
	if err != nil {
		return err
	}
	// Only the repository's strict domain normalization may acknowledge runtime
	// or resources. A matching but partial observation retains the held ledger.
	if _, err = u.labs.RecordObservation(ctx, lab.ID, observation); err != nil {
		return err
	}
	current, err := u.labs.Get(ctx, lab.ID)
	if err != nil {
		return err
	}
	if current.OperationID != lab.OperationID || current.Revision != lab.Revision || current.DesiredState != "Stopped" {
		return nil
	}
	if current.ActualState == "Stopped" && current.ObservedRevision == current.Revision && current.AccessFenced && current.Allocation.RuntimeState == "Released" && current.Allocation.ReleasedAt != nil && current.FailureCode == "" && (current.SnapshotMode == "skip" || current.SnapshotState == "Succeeded") {
		return nil
	}
	// Producer stopped intent fences access itself before required capture. ACL
	// acceptance is never proof of that physical access fence.
	if err = port.StopLab(ctx, eventLabModel.StopRequest{Target: eventLabModel.Target{Ref: current.Ref, ExpectedUID: current.AgentUID, OperationID: current.OperationID, Revision: current.Revision}, SnapshotMode: current.SnapshotMode, RetentionUntil: current.EffectiveRetentionUntil(), Terminal: current.CloseReason == "solved"}); err != nil {
		return err
	}
	if current.FailureCode != "" {
		return fmt.Errorf("producer lifecycle failure %s", current.FailureCode)
	}
	return nil
}

func (u *EventUseCase) reconcileRunningLab(ctx context.Context, l eventLabModel.Lab, now time.Time) error {
	port, ok := u.infra.(LabLifecycleInfrastructure)
	if !ok {
		return infraUnavailable()
	}
	o, err := port.ObserveLab(ctx, l.Ref)
	if err != nil {
		return err
	}
	if l.AgentUID == "" {
		adopted, err := u.labs.RecordBirthIdentity(ctx, l.ID, o, now)
		if err != nil {
			return err
		}
		if !adopted {
			return nil
		}
	}
	if _, err = u.labs.RecordObservation(ctx, l.ID, o); err != nil {
		return err
	}
	current, err := u.labs.Get(ctx, l.ID)
	if err != nil {
		return err
	}
	if current.Revision != l.Revision || current.OperationID != l.OperationID || current.DesiredState != "Running" {
		return nil
	}
	if current.RuntimeReady && current.ActualState == "Running" && current.ObservedRevision == current.Revision {
		return nil
	}
	if current.Revision == 1 && current.CreateEvidence != nil {
		return nil
	}
	if current.AgentUID == "" || !u.groupAllowsDeployment(ctx, current.TeamID, now) {
		return nil
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return port.StartLab(commandCtx, eventLabModel.Target{Ref: current.Ref, ExpectedUID: current.AgentUID, OperationID: current.OperationID, Revision: current.Revision})
}
