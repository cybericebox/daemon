package event

import (
	"context"
	"time"

	eventFormRepo "github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/model"
)

const dueFormAssignmentBatchSize = 100

// MaterializeDueFormDeliveries is the schedule-driven counterpart to the
// signal hook. It materializes explicit timed assignments and the one-time
// event_finished trigger once the event's effective finish time is reached.
// Selection, delivery writes, and materialization marking share one database
// transaction, so a worker crash leaves the assignment eligible for retry.
func (u *EventUseCase) MaterializeDueFormDeliveries(ctx context.Context) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()

	now := time.Now().UTC()
	forms := eventFormRepo.New(txRepo)
	assignments, err := forms.ListDueTimedAssignmentsForUpdate(txCtx, now, dueFormAssignmentBatchSize)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list due form assignments").Err()
	}
	store := NewFormDeliveryRepositoryStore(forms, func() time.Time { return now })
	for _, item := range assignments {
		assignment := FormDeliveryAssignment{ID: item.ID, Assignment: item.Rule}
		version, err := forms.LatestByFormID(txCtx, item.FormID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to resolve due form version").Err()
		}
		assignment.FormVersionID = version.ID
		if err = MaterializeFormDeliveries(txCtx, store, item.EventID, assignment, nil); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to materialize due form deliveries").Err()
		}
		if err = forms.MarkAssignmentMaterialized(txCtx, item.ID, now); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to mark form assignment materialized").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save due form deliveries").Err()
	}
	return nil
}
