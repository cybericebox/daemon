package notifyJob

import (
	"context"
	"encoding/json"

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
}
type notifyWorker struct {
	river.WorkerDefaults[jobsModel.NotifyArgs]
	uc IUseCase
}

func (w *notifyWorker) Work(ctx context.Context, job *river.Job[jobsModel.NotifyArgs]) error {
	data := map[string]any{}
	if len(job.Args.Vars) > 0 {
		if err := json.Unmarshal(job.Args.Vars, &data); err != nil {
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
			Recipient:        job.Args.Recipient,
			TemplateID:       job.Args.TemplateID,
			ScopeEventID:     job.Args.ScopeEventID,
			Inbox:            job.Args.Inbox,
			BroadcastID:      job.Args.BroadcastID,
		},
	)
	// A send limit is not a failure: run again once the limit allows.
	if d, ok := dispatchModel.AsDeferred(err); ok {
		return river.JobSnooze(d.RetryAfter)
	}
	return err
}

// NewWorker builds this job's River worker from its narrow use-case port. The
// central worker registry imports this constructor and adds it to the bundle.
func NewWorker(uc IUseCase) *notifyWorker {
	return &notifyWorker{uc: uc}
}
