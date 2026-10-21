package errorJournalUseCase

import (
	"context"
	"errors"
	"fmt"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/gofrs/uuid"
	"time"
)

const deliveryTimeout = 20 * time.Second

// ErrDeliveryGone means retention removed the delivery; a queued replay has no work left.
var ErrDeliveryGone = errors.New("journal delivery no longer exists")

// NotificationPlan is persisted in the capture transaction. Recipient lookup and network calls happen later.
type NotificationPlan struct {
	Now, Cutoff     time.Time
	Body, EmailBody string
	// ThrottleKind, when set, limits the messages of the whole kind (not one group) to one per cooldown: the
	// repository drops the plan inside the capture transaction when a group of that kind was enqueued after
	// ThrottleCutoff. It is a database decision, so replicas share it.
	ThrottleKind   errorJournal.Kind
	ThrottleCutoff time.Time
}
type DeliveryTarget struct{ Channel, Target, Label string }

// Delivery is the operator read model; message bodies and lease tokens are never returned over HTTP.
type Delivery struct {
	ID, GroupID                             uuid.UUID
	Channel, Target, Label, Body, EmailBody string
	Attempts                                int
	LastError                               string
	CreatedAt, NextAttemptAt                time.Time
	LastAttemptAt, DeliveredAt, CancelledAt *time.Time
}

// ProcessJournalDelivery is executed by the same River notify worker as ordinary messages.
// SMTP and Telegram do not offer idempotency keys: a crash after provider acceptance but before
// recording success can duplicate that one target. Confirmed successful targets are skipped on replay.
func (j *Journal) ProcessJournalDelivery(ctx context.Context, id uuid.UUID) error {
	d, err := j.repo.GetDelivery(ctx, id)
	if errors.Is(err, ErrDeliveryGone) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.DeliveredAt != nil || d.CancelledAt != nil {
		return nil
	}
	if err := j.repo.StartDelivery(ctx, id, j.now()); err != nil {
		return err
	}
	d.Attempts++
	if d.Channel == "prepare" {
		targets, err := j.deliveryTargets(ctx)
		if err == nil {
			err = j.repo.ExpandDelivery(ctx, d, targets, j.now())
		}
		if err == nil {
			return nil
		}
		return j.completeDelivery(ctx, d, TestResult{Error: errorJournal.Clean(err.Error(), 300)})
	}
	var result TestResult
	switch d.Channel {
	case ChannelTelegram:
		if j.tg == nil || !j.tg.Enabled() {
			result.Error = "Telegram transport unavailable"
		} else {
			result = j.sendTelegram(ctx, errorJournal.TelegramChat{ChatID: d.Target, Label: d.Label, Failing: true}, d.Body)
		}
	case ChannelEmail:
		eligible, err := j.deliveryEmailEligible(ctx, d.Target)
		if err != nil {
			return j.completeDelivery(ctx, d, TestResult{Error: errorJournal.Clean(err.Error(), 300)})
		}
		if !eligible {
			now := j.now()
			d.CancelledAt = &now
			return j.completeDelivery(ctx, d, TestResult{Error: "recipient is no longer active, confirmed, or configured"})
		}
		if j.mail == nil {
			result.Error = "Email transport unavailable"
		} else {
			result = j.sendEmail(ctx, d.Target, d.Body)
		}
	default:
		result.Error = "Unknown notification channel"
	}
	return j.completeDelivery(ctx, d, result)
}

func (j *Journal) completeDelivery(ctx context.Context, d Delivery, result TestResult) error {
	now := j.now()
	delay := min(30*time.Second*time.Duration(1<<min(max(d.Attempts-1, 0), 5)), 15*time.Minute)
	if err := j.repo.FinishDelivery(ctx, d, result, now, now.Add(delay)); err != nil {
		return err
	}
	if !result.OK && d.CancelledAt == nil {
		return &dispatchModel.DeferredError{Message: result.Error, RetryAfter: delay}
	}
	return nil
}

func (j *Journal) deliveryTargets(ctx context.Context) ([]DeliveryTarget, error) {
	settings, err := j.repo.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	var targets []DeliveryTarget
	if j.tg != nil && j.tg.Enabled() {
		for _, chat := range settings.TelegramChats {
			targets = append(targets, DeliveryTarget{ChannelTelegram, chat.ChatID, chat.Label})
		}
	}
	if j.mail != nil {
		recipients, err := j.recipientsEligible(ctx, settings)
		if err != nil {
			return nil, err
		}
		for _, to := range recipients {
			targets = append(targets, DeliveryTarget{Channel: ChannelEmail, Target: to})
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no error notification recipients or enabled transports")
	}
	return targets, nil
}
