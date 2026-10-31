package dispatcherUseCase

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

// ResendDispatch sends the failed channels of a dispatch again from its stored content. Channels that were
// delivered are not touched. The content is kept while the notification job still has retries or for the payload
// lifetime after it gave up; past that the dispatch cannot be resent.
func (u *NotificationDispatcher) ResendDispatch(ctx context.Context, dispatchID uuid.UUID) error {
	info, err := u.dispatches.Get(ctx, dispatchID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return notificationModel.ErrDispatchNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to load the dispatch").Err()
	}
	targets, err := u.dispatches.ListTargets(ctx, dispatchID)
	if err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to load the dispatch targets").
			Err()
	}
	var failed []string
	for _, t := range targets {
		if t.Status == string(dispatchModel.TargetStatusError) {
			failed = append(failed, t.Channel)
		}
	}
	if len(failed) == 0 {
		return notificationModel.ErrDispatchNotFailed.Err()
	}
	if _, err = u.LoadNotificationPayload(ctx, dispatchID); err != nil {
		if errors.Is(err, dispatchModel.ErrPayloadGone) {
			return notificationModel.ErrDispatchPayloadGone.Err()
		}
		return err
	}

	previous := dispatchModel.DispatchStatus(info.Status)
	if err = u.dispatches.SetStatus(
		ctx,
		dispatchID,
		dispatchModel.DispatchStatusPending,
	); err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to mark the dispatch pending").
			Err()
	}
	args := jobsModel.NotifyArgs{
		DispatchID: dispatchID, UserID: info.RecipientUserID, Type: info.NotificationType,
		OverrideChannels: failed, ScopeEventID: info.ScopeEventID, BroadcastID: info.BroadcastID,
	}
	if err = u.enqueuer.Enqueue(ctx, args); err != nil {
		// Keep the journal truthful: the dispatch is as failed as before.
		_ = u.dispatches.SetStatus(ctx, dispatchID, previous)
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to queue the notification again").
			Err()
	}
	return nil
}
