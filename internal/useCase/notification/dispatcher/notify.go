package dispatcherUseCase

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	"github.com/cybericebox/daemon/internal/model"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/pkg/tools"
	"github.com/cybericebox/daemon/pkg/worker"
)

// Notify creates the dispatch row and enqueues the job. The template vars are
// passed as a domain map; JSON marshaling happens at the worker (transport) boundary.
func (u *NotificationDispatcher) Notify(
	ctx context.Context,
	userID uuid.UUID,
	n notificationTypes.NotificationPayload,
	opts ...dispatchModel.NotifyOption,
) error {
	o := dispatchModel.ApplyNotifyOptions(opts)
	dispatchID := tools.NewUUIDv7()
	if o.DispatchID != nil {
		dispatchID = *o.DispatchID
	}

	vars, err := n.Marshal()
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to marshal notification vars").Err()
	}

	override := make([]string, 0, len(o.OverrideChannels))
	for _, ch := range o.OverrideChannels {
		override = append(override, string(ch))
	}

	if u.unitOfWorker == nil {
		return model.ErrPlatform.WithMessage("Notification transaction is not configured").Err()
	}
	queue, ok := u.enqueuer.(worker.TransactionalEnqueuer)
	if !ok {
		return model.ErrPlatform.WithMessage("Transactional notification queue is not configured").Err()
	}

	txCtx := ctx
	var repo Repository
	var unit postgres.UoW
	tx, ok := postgres.TransactionFromContext(ctx)
	if ok {
		repo = postgres.New(tx)
	} else {
		txCtx, repo, unit, err = u.unitOfWorker.UnitOfWork(ctx)
		if err != nil {
			return err
		}
		defer unit.Restore()
		tx, ok = postgres.TransactionFromContext(txCtx)
		if !ok {
			return model.ErrPlatform.WithMessage("Notification transaction is missing").Err()
		}
	}
	if o.DispatchID != nil {
		created, err := dispatchRepo.New(repo).CreateIfAbsent(txCtx, dispatchID, string(n.NotificationType()), userID, o.ScopeEventID, o.BroadcastID)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
	} else {
		if err := dispatchRepo.New(repo).CreateForBroadcast(txCtx, dispatchID, string(n.NotificationType()), userID, o.ScopeEventID, o.BroadcastID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create dispatch").Err()
		}
	}

	payloads := &payloadStore{codes: temporalCodeRepo.New(repo), cipher: u.payloads.cipher}
	if err := payloads.put(txCtx, dispatchID, userID, dispatchModel.Payload{Vars: vars, Recipient: o.Recipient, Inbox: o.Inbox}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to store notification payload").Err()
	}
	if err := queue.EnqueueTx(txCtx, tx, jobsModel.NotifyArgs{DispatchID: dispatchID, UserID: userID, Type: string(n.NotificationType()), OverrideChannels: override, TemplateID: o.TemplateID, ScopeEventID: o.ScopeEventID, BroadcastID: o.BroadcastID}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to enqueue notification").Err()
	}
	if unit != nil {
		return unit.Save()
	}
	return nil
}
