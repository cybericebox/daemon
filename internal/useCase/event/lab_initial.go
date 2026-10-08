package event

import (
	"context"
	"fmt"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
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
	if l.AgentUID == "" {
		if status.LabUID == "" || status.LabGeneration <= 0 {
			return false, nil
		}
		adopted, err := u.labs.RecordInitialIdentity(ctx, l.ID, l.Ref, status.LabUID, status.LabGeneration, now)
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
	port, ok := u.infra.(LabLifecycleInfrastructure)
	if !ok {
		return false, fmt.Errorf("managed initial lifecycle observation unavailable")
	}
	o, err := port.ObserveLab(ctx, l.Ref)
	if err != nil {
		return false, err
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
	if o.UID == current.AgentUID && o.OperationID == uuid.Nil {
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
