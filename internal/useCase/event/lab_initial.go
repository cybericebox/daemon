package event

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/gofrs/uuid"
	"time"
)

// Initial managed readiness is the exact persisted Running intent. Generic pod
// Ready only supplies the identity for adoption; it never certifies allocation.
func (u *EventUseCase) observeInitialManagedLab(ctx context.Context, binding labBindingModel.Binding, status exerciseModel.LabDeployStatus, now time.Time) (bool, error) {
	if !binding.LabID.Valid {
		return false, nil
	}
	l, err := u.labs.Get(ctx, binding.LabID.UUID)
	if err != nil {
		return false, err
	}
	if l.DesiredState != "Running" || l.ClosedAt != nil || l.Revision != 1 || l.Ref.Group != binding.LabGroupName || l.Ref.Lab != binding.LabName {
		return false, nil
	}
	port, ok := u.infra.(LabLifecycleInfrastructure)
	if !ok {
		return false, fmt.Errorf("managed initial lifecycle observation unavailable")
	}
	o, err := port.ObserveLab(ctx, l.Ref)
	if err != nil {
		return false, err
	}
	if l.AgentUID == "" {
		adopted, err := u.labs.RecordBirthIdentity(ctx, l.ID, o, now)
		if err != nil {
			return false, err
		}
		if !adopted {
			return false, nil
		}
		l, err = u.labs.Get(ctx, l.ID)
		if err != nil {
			return false, err
		}
	}
	if status.LabUID != l.AgentUID {
		return false, nil
	}
	if _, err = u.labs.RecordObservation(ctx, l.ID, o); err != nil {
		return false, err
	}
	current, err := u.labs.Get(ctx, l.ID)
	if err != nil {
		return false, err
	}
	if current.OperationID != l.OperationID || current.Revision != l.Revision || current.DesiredState != "Running" || current.ClosedAt != nil {
		return false, nil
	}
	certified := eventLabModel.ObservationMatches(current, o) && o.DesiredState == "Running" && o.ActualState == "Running" && o.RuntimeReady && o.Allocation.RuntimeState == "Allocated" && o.ObservedAt != nil && !o.ObservedAt.After(now) && now.Sub(*o.ObservedAt) <= 30*time.Second && current.RuntimeReady && current.ObservedRevision == current.Revision
	if certified {
		return true, nil
	}
	// An existing lifecycle-less copy can adopt the same persisted initial intent
	// only through a current UID-fenced qualified command. Acceptance stays pending.
	if current.CreateEvidence == nil && o.Creation == nil && o.UID == current.AgentUID && o.OperationID == uuid.Nil {
		caps, known := u.labCaps(ctx, current.Ref.Group)
		if known && caps.ConfirmedRuntime && caps.RetainedRestart {
			commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err = port.StartLab(commandCtx, eventLabModel.Target{Ref: current.Ref, ExpectedUID: current.AgentUID, OperationID: current.OperationID, Revision: current.Revision}); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

func (u *EventUseCase) recordInitialCreateDispatch(ctx context.Context, initial eventLabModel.Lab, dispatch infraModel.LabCreateDispatch) error {
	if u.uow == nil {
		return fmt.Errorf("managed create identity transaction unavailable")
	}
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, initial.EventID); err != nil {
		return err
	}
	if err = eventLabRepo.New(q).LockAdmission(txCtx, initial.TeamID); err != nil {
		return err
	}
	l, err := eventLabRepo.New(q).Lock(txCtx, initial.ID)
	if err != nil {
		return err
	}
	if l.Ref != initial.Ref || l.Generation != initial.Generation || !l.RecordCreateDispatch(dispatch.GroupUID, dispatch.DefinitionHash, initial.OperationID, time.Now().UTC()) {
		return eventLabModel.ErrAccessClosed.Err()
	}
	updated, err := eventLabRepo.New(q).Update(txCtx, l, l.Revision)
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("managed create intent changed")
	}
	return unit.Save()
}
