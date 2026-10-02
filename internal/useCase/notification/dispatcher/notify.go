package dispatcherUseCase

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/pkg/tools"
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

	if err := u.dispatches.CreateForBroadcast(ctx, dispatchID, string(n.NotificationType()), userID, o.ScopeEventID, o.BroadcastID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create dispatch").Err()
	}
	vars, err := n.Marshal()
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to marshal notification vars").Err()
	}

	override := make([]string, 0, len(o.OverrideChannels))
	for _, ch := range o.OverrideChannels {
		override = append(override, string(ch))
	}

	// Names, addresses and links go into a sealed row; the job carries ids only.
	if err = u.payloads.put(ctx, dispatchID, userID, dispatchModel.Payload{Vars: vars, Recipient: o.Recipient, Inbox: o.Inbox}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to store notification payload").Err()
	}
	if err = u.enqueuer.Enqueue(
		ctx, jobsModel.NotifyArgs{
			DispatchID:       dispatchID,
			UserID:           userID,
			Type:             string(n.NotificationType()),
			OverrideChannels: override,
			TemplateID:       o.TemplateID,
			ScopeEventID:     o.ScopeEventID,
			BroadcastID:      o.BroadcastID,
		},
	); err != nil {
		_ = u.payloads.drop(ctx, dispatchID)
		return model.ErrPlatform.WithError(err).WithMessage("Failed to enqueue notification").Err()
	}
	return nil
}
