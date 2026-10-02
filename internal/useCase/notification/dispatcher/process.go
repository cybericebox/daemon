package dispatcherUseCase

import (
	"context"
	"strconv"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/model"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

func (u *NotificationDispatcher) ProcessNotification(
	ctx context.Context,
	in dispatchModel.ProcessInput,
) error {
	if err := u.dispatches.SetStatus(ctx, in.DispatchID, dispatchModel.DispatchStatusStarted); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to mark dispatch started").Err()
	}

	var recipient userModel.User
	if in.Recipient != nil {
		recipient = *in.Recipient
	} else {
		user, err := u.users.GetByID(ctx, in.UserID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to get recipient user").Err()
		}
		recipient = user
	}

	// A broadcast dispatch renders the authored content instead of a template.
	if in.BroadcastID != nil {
		content, err := u.broadcasts.Content(ctx, *in.BroadcastID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to load broadcast content").Err()
		}
		ctx = dispatchModel.WithBroadcast(ctx, &content)
	}

	// Resolve the candidate channels. Override (test-send / forced) replaces the
	// global+user preference resolution entirely (L2/L3 bypassed); the L1
	// Supports gate below still applies, so unsupported/garbage channels are skipped.
	var candidates []notificationTypes.NotificationChannel
	if len(in.OverrideChannels) > 0 {
		candidates = in.OverrideChannels
	} else {
		chans, err := u.dispatches.ActiveChannels(ctx, in.UserID, in.Type)
		if err != nil {
			return model.ErrPlatform.WithError(err).
				WithMessage("Failed to resolve active channels").
				Err()
		}
		candidates = chans
	}

	// Build the worklist: candidate channels that pass the L1 Supports gate and
	// have a wired handler. (Supports + handler nil-check stay here, once.)
	type pendingTarget struct {
		ch notificationTypes.NotificationChannel
		h  Handler
	}
	worklist := make([]pendingTarget, 0, len(candidates))
	for _, ch := range candidates {
		if !notificationTypes.Supports(notificationTypes.NotificationType(in.Type), ch) {
			continue
		}
		if h := u.handlers[ch]; h != nil {
			worklist = append(worklist, pendingTarget{ch: ch, h: h})
		}
	}

	// A deferred email is run again later as the whole job: channels that
	// already finished (the in-app copy) must not be delivered twice.
	if len(worklist) > 1 {
		targets, err := u.dispatches.ListTargets(ctx, in.DispatchID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to load dispatch targets").Err()
		}
		done := make(map[notificationTypes.NotificationChannel]bool, len(targets))
		for _, t := range targets {
			if t.Status == string(dispatchModel.TargetStatusDone) {
				done[notificationTypes.NotificationChannel(t.Channel)] = true
			}
		}
		pending := worklist[:0]
		for _, p := range worklist {
			if !done[p.ch] {
				pending = append(pending, p)
			}
		}
		worklist = pending
	}

	// Round-based retry: round 0 attempts every channel once; each later round
	// re-attempts only the channels that failed with a retryable error, after
	// waiting retryDelay. ErrTemplateNotFound is terminal and never retried.
	lastErr := make(map[notificationTypes.NotificationChannel]error, len(worklist))
	notes := make(map[notificationTypes.NotificationChannel]*dispatchModel.DeliveryNote, len(worklist))
	attempts := make(map[notificationTypes.NotificationChannel]int32, len(worklist))
	remaining := worklist
	for round := 0; round < maxDispatchRounds && len(remaining) > 0; round++ {
		if ctx.Err() != nil {
			break
		}
		if round > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(u.retryDelay):
			}
			if ctx.Err() != nil {
				break // context cancelled: stop retrying, keep last errors
			}
		}
		var failed []pendingTarget
		for _, p := range remaining {
			// A fresh note per attempt: the journal keeps the final route.
			note := &dispatchModel.DeliveryNote{}
			notes[p.ch] = note
			attempts[p.ch]++
			hctx := dispatchModel.WithInboxMeta(dispatchModel.WithDeliveryNote(ctx, note), in.Inbox)
			var hErr error
			if in.ScopeEventID == nil {
				hErr = p.h.Handle(hctx, recipient, notificationTypes.NotificationType(in.Type), in.Vars, in.TemplateID)
			} else {
				hErr = p.h.Handle(hctx, recipient, notificationTypes.NotificationType(in.Type), in.Vars, in.TemplateID, in.ScopeEventID)
			}
			attempts[p.ch] += note.ExtraAttempts
			lastErr[p.ch] = hErr
			if _, deferred := dispatchModel.AsDeferred(hErr); deferred {
				attempts[p.ch]-- // nothing was sent: not a delivery attempt
				continue         // waits for its send limit, not for a retry round
			}
			if hErr != nil && !notificationModel.ErrTemplateNotFound.Err().Is(hErr) {
				failed = append(failed, p) // retryable failure
			}
		}
		remaining = failed
	}

	// Record the final per-channel status.
	var deferred *dispatchModel.DeferredError
	for _, p := range worklist {
		status, msg := dispatchModel.TargetStatusDone, ""
		if hErr := lastErr[p.ch]; hErr != nil {
			if d, ok := dispatchModel.AsDeferred(hErr); ok {
				status, msg = dispatchModel.TargetStatusDeferred, d.Message
				if deferred == nil || d.RetryAfter > deferred.RetryAfter {
					deferred = d
				}
			} else if notificationModel.ErrTemplateNotFound.Err().Is(hErr) {
				status, msg = dispatchModel.TargetStatusError, "no template"
			} else {
				status, msg = dispatchModel.TargetStatusError, hErr.Error()
				reportMailFailure(p.ch, in, hErr, attempts[p.ch])
			}
		}
		result := dispatchRepo.TargetResult{Status: status, Error: msg, Attempts: attempts[p.ch]}
		if note := notes[p.ch]; note != nil {
			result.Note = *note
		}
		_ = u.dispatches.UpsertTarget(ctx, in.DispatchID, p.ch, result)
	}

	if deferred != nil {
		// The job is snoozed; the dispatch stays started until it finishes.
		return deferred
	}
	if err := u.dispatches.SetStatus(ctx, in.DispatchID, dispatchModel.DispatchStatusDone); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to mark dispatch done").Err()
	}
	return nil
}

// reportMailFailure tells the error journal about an e-mail that failed after its retry rounds. Other channels
// (the in-app copy) are not mail. The error text is scrubbed by the journal: it can hold an address.
func reportMailFailure(ch notificationTypes.NotificationChannel, in dispatchModel.ProcessInput, err error, attempts int32) {
	if ch != notificationTypes.NotificationChannelEmail {
		return
	}
	errorJournal.Report(errorJournal.Event{
		Kind: errorJournal.KindMail, Source: "email", Message: err.Error(),
		Details: map[string]string{
			"notification_type": in.Type, "dispatch_id": in.DispatchID.String(), "attempts": strconv.Itoa(int(attempts)),
		},
	})
}
