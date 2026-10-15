package notifyJob

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"

	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// notifyWorker is the consumer: it decodes the job envelope and hands it to the
// use-case via the package's hidden useCase port.

type IUseCase interface {
	ProcessNotification(ctx context.Context, in dispatchModel.ProcessInput) error
	// LoadNotificationPayload reads the sealed variables of a queued notification; ErrPayloadGone when
	// they no longer exist.
	LoadNotificationPayload(ctx context.Context, dispatchID uuid.UUID) (dispatchModel.Payload, error)
	DiscardNotificationPayload(ctx context.Context, dispatchID uuid.UUID) error
}
type notifyWorker struct {
	river.WorkerDefaults[jobsModel.NotifyArgs]
	uc IUseCase
}

// NextRetry bounds the retry horizon below the sealed payload's 14-day retention.
// The normal 25-attempt River limit still makes terminal failures inspectable as discarded jobs.
func (w *notifyWorker) NextRetry(job *river.Job[jobsModel.NotifyArgs]) time.Time {
	return time.Now().Add(notifyRetryDelay(job.Attempt))
}
func notifyRetryDelay(attempt int) time.Duration {
	return min(30*time.Second*time.Duration(1<<min(max(attempt-1, 0), 10)), 6*time.Hour)
}

func (w *notifyWorker) Work(ctx context.Context, job *river.Job[jobsModel.NotifyArgs]) error {
	if job.Args.AuthDeliveryID != nil {
		processor, ok := w.uc.(interface {
			ProcessAuthDelivery(context.Context, uuid.UUID) error
		})
		if !ok {
			return errors.New("auth delivery processor unavailable")
		}
		return processor.ProcessAuthDelivery(ctx, *job.Args.AuthDeliveryID)
	}
	if job.Args.JournalDeliveryID != nil {
		processor, ok := w.uc.(interface {
			ProcessJournalDelivery(context.Context, uuid.UUID) error
		})
		if !ok {
			return errors.New("journal delivery processor unavailable")
		}
		err := processor.ProcessJournalDelivery(ctx, *job.Args.JournalDeliveryID)
		if d, ok := dispatchModel.AsDeferred(err); ok {
			return river.JobSnooze(d.RetryAfter)
		}
		return err
	}

	// The arguments hold ids only. A job queued before that still carries its variables: it runs from them.
	vars, recipient, inbox := job.Args.Vars, job.Args.Recipient, job.Args.Inbox
	legacy := len(vars) > 0 || recipient != nil || inbox != nil
	if !legacy {
		payload, err := w.uc.LoadNotificationPayload(ctx, job.Args.DispatchID)
		if err != nil {
			if errors.Is(err, dispatchModel.ErrPayloadGone) {
				// Nothing to send and nothing to retry with: stop this job once.
				return river.JobCancel(err)
			}
			return err
		}
		vars, recipient, inbox = payload.Vars, payload.Recipient, payload.Inbox
	}
	data := map[string]any{}
	if len(vars) > 0 {
		if err := json.Unmarshal(vars, &data); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to decode vars").Err()
		}
	}
	override := make([]notificationTypes.NotificationChannel, 0, len(job.Args.OverrideChannels))
	for _, ch := range job.Args.OverrideChannels {
		override = append(override, notificationTypes.NotificationChannel(ch))
	}

	err := w.uc.ProcessNotification(
		ctx, dispatchModel.ProcessInput{
			DispatchID:       job.Args.DispatchID,
			UserID:           job.Args.UserID,
			Type:             job.Args.Type,
			Vars:             data,
			OverrideChannels: override,
			Recipient:        recipient,
			TemplateID:       job.Args.TemplateID,
			ScopeEventID:     job.Args.ScopeEventID,
			Inbox:            inbox,
			BroadcastID:      job.Args.BroadcastID,
		},
	)
	// A send limit is not a failure: run again once the limit allows.
	if d, ok := dispatchModel.AsDeferred(err); ok {
		return river.JobSnooze(d.RetryAfter)
	}
	if err == nil && !legacy {
		_ = w.uc.DiscardNotificationPayload(ctx, job.Args.DispatchID)
	}
	return err
}

// NewWorker builds this job's River worker from its narrow use-case port. The
// central worker registry imports this constructor and adds it to the bundle.
func NewWorker(uc IUseCase) *notifyWorker {
	return &notifyWorker{uc: uc}
}
