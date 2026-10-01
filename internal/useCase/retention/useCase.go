// Package retention enforces the data retention periods of the Privacy Policy
// ("Retention"): it purges data past its period and removes inactive
// accounts after an email warning.
package retention

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
)

// deletionDateLayout is the date format of the warning email (uk templates).
const deletionDateLayout = "02.01.2006"

type (
	// Store is the retention statement port (satisfied by *retentionRepo.Repository).
	Store interface {
		PurgeExpiredSessions(ctx context.Context, expiredBefore time.Time, batchSize int32) (int64, error)
		PurgeEventLabObservations(ctx context.Context, observedBefore time.Time, batchSize int32) (int64, error)
		PurgePlatformLabCapacityObservations(ctx context.Context, observedBefore time.Time, batchSize int32) (int64, error)
		PurgeNotificationDispatches(ctx context.Context, createdBefore time.Time, batchSize int32) (int64, error)
		PurgeFinishedSignals(ctx context.Context, createdBefore time.Time, batchSize int32) (int64, error)
		PurgeEventAnalytics(ctx context.Context, endedBefore time.Time, batchSize int32) (int64, error)
		PurgeEventFormAnswers(ctx context.Context, endedBefore time.Time, batchSize int32) (int64, error)
		PurgeEventAnswerFiles(ctx context.Context, endedBefore, unusedBefore time.Time, batchSize int32) (int64, error)
		PurgeDeletedAccounts(ctx context.Context, now time.Time, batchSize int32) (int64, error)
		ClearReturnedInactivityWarnings(ctx context.Context) (int64, error)
		ListInactiveAccountsToWarn(ctx context.Context, inactiveSince time.Time, batchSize int32) ([]retentionModel.InactiveAccount, error)
		MarkInactivityWarned(ctx context.Context, userID uuid.UUID, warnedAt time.Time) (bool, error)
		ListInactiveAccountsToDelete(ctx context.Context, warnedBefore time.Time, batchSize int32) ([]retentionModel.WarnedAccount, error)
		PurgeExpiredEventInvitations(ctx context.Context, expiredBefore time.Time, batchSize int32) (int64, error)
		PurgeUnconfirmedAccounts(ctx context.Context, createdBefore, now time.Time, batchSize int32) (int64, error)
	}

	// Notifier is the dispatch port (satisfied by the aggregate *NotificationDispatcher).
	Notifier interface {
		Notify(ctx context.Context, userID uuid.UUID, n notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error
	}

	// AccountRemover deletes an account through the deletion-request cascade
	// (satisfied by *auth.AuthUseCase).
	AccountRemover interface {
		DeleteInactiveAccount(ctx context.Context, userID uuid.UUID, warnedAt time.Time) (bool, error)
	}

	Dependencies struct {
		Store    Store
		Notifier Notifier
		Accounts AccountRemover
		Policy   retentionModel.Policy
		// SignInURL is the ID sign-in page linked from the warning email.
		SignInURL string
		// Now is the clock; nil means time.Now.
		Now func() time.Time
	}

	RetentionUseCase struct {
		store     Store
		notifier  Notifier
		accounts  AccountRemover
		policy    retentionModel.Policy
		signInURL string
		now       func() time.Time
	}
)

func New(deps Dependencies) *RetentionUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &RetentionUseCase{
		store: deps.Store, notifier: deps.Notifier, accounts: deps.Accounts,
		policy: deps.Policy, signInURL: deps.SignInURL, now: now,
	}
}

// EnforceDataRetention purges every kind of data past its period, and the
// residual personal data of deleted accounts. A failing step does not stop
// the others; the run logs one line with the counts and returns the failures.
func (u *RetentionUseCase) EnforceDataRetention(ctx context.Context) error {
	started := u.now()
	cut := u.policy.Cutoffs(started)
	batch := u.policy.BatchSize
	var errs []error
	step := func(purge func(context.Context) (int64, error)) int64 {
		n, err := drain(ctx, batch, purge)
		if err != nil {
			errs = append(errs, err)
		}
		return n
	}

	sessions := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeExpiredSessions(ctx, cut.SessionsExpiredBefore, batch)
	})
	labObservations := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeEventLabObservations(ctx, cut.LabObservedBefore, batch)
	})
	capacityObservations := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgePlatformLabCapacityObservations(ctx, cut.LabObservedBefore, batch)
	})
	dispatches := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeNotificationDispatches(ctx, cut.DispatchesCreatedBefore, batch)
	})
	signals := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeFinishedSignals(ctx, cut.SignalsCreatedBefore, batch)
	})
	analytics := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeEventAnalytics(ctx, cut.AnalyticsEventsEndedBefore, batch)
	})
	formAnswers := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeEventFormAnswers(ctx, cut.EventsEndedBefore, batch)
	})
	deletedAccounts := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeDeletedAccounts(ctx, started, batch)
	})
	// After deleted accounts: the references their answer files leave behind
	// are released in the same run.
	answerFiles := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeEventAnswerFiles(ctx, cut.EventsEndedBefore, cut.UnusedAnswerFilesCreatedBefore, batch)
	})
	// Expired invitations of confirmed accounts go after a grace period; an
	// unconfirmed account goes 30 days after creation unless its invitation
	// is still live (one rule for invited accounts and self sign-ups).
	invitations := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeExpiredEventInvitations(ctx, cut.InvitationsExpiredBefore, batch)
	})
	unconfirmedAccounts := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeUnconfirmedAccounts(ctx, cut.PendingAccountsCreatedBefore, started, batch)
	})

	err := errors.Join(errs...)
	log.Info().Err(err).
		Int64("sessions", sessions).
		Int64("lab_observations", labObservations).
		Int64("lab_capacity_observations", capacityObservations).
		Int64("notification_dispatches", dispatches).
		Int64("signals", signals).
		Int64("event_analytics", analytics).
		Int64("form_answers", formAnswers).
		Int64("answer_files", answerFiles).
		Int64("deleted_accounts_purged", deletedAccounts).
		Int64("expired_invitations", invitations).
		Int64("unconfirmed_accounts", unconfirmedAccounts).
		Dur("took", u.now().Sub(started)).
		Msg("data retention: purge run")
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to enforce data retention").Err()
	}
	return nil
}

// EnforceAccountInactivity warns the owners of accounts inactive for the
// policy period and deletes the accounts still inactive a grace period after
// the warning. Signing in again voids the warning. The deletion goes through
// the deletion-request cascade; the next purge run removes the residual
// personal data and keeps competition results under the anonymized tombstone.
func (u *RetentionUseCase) EnforceAccountInactivity(ctx context.Context) error {
	started := u.now()
	cut := u.policy.Cutoffs(started)

	cleared, err := u.store.ClearReturnedInactivityWarnings(ctx)
	var warned, deleted, kept int64
	if err == nil {
		warned, err = u.warnInactiveAccounts(ctx, started, cut.InactiveSince)
	}
	if err == nil {
		deleted, kept, err = u.deleteWarnedAccounts(ctx, cut.WarnedBefore)
	}

	log.Info().Err(err).
		Int64("warnings_cleared", cleared).
		Int64("warned", warned).
		Int64("deleted", deleted).
		Int64("kept_active", kept).
		Dur("took", u.now().Sub(started)).
		Msg("data retention: account inactivity run")
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to enforce account inactivity").Err()
	}
	return nil
}

// warnInactiveAccounts queues the warning email, then records it. A failure
// stops the run (the unmarked account is listed again next time), so an
// account is never deleted without a queued warning.
func (u *RetentionUseCase) warnInactiveAccounts(ctx context.Context, now, inactiveSince time.Time) (int64, error) {
	deletionDate := u.policy.DeletionDate(now).Format(deletionDateLayout)
	var warned int64
	for {
		accounts, err := u.store.ListInactiveAccountsToWarn(ctx, inactiveSince, u.policy.BatchSize)
		if err != nil {
			return warned, err
		}
		for _, account := range accounts {
			if err = u.notifier.Notify(ctx, account.UserID, notificationPayloads.AccountInactivityWarningPayload{
				Name: account.FirstName, DeletionDate: deletionDate, SignInURL: u.signInURL,
			}); err != nil {
				return warned, err
			}
			marked, err := u.store.MarkInactivityWarned(ctx, account.UserID, now)
			if err != nil {
				return warned, err
			}
			if marked {
				warned++
			}
		}
		if len(accounts) < int(u.policy.BatchSize) {
			return warned, nil
		}
	}
}

// deleteWarnedAccounts deletes the accounts whose grace period is over. An
// account whose owner signed in meanwhile is kept (and no longer listed).
func (u *RetentionUseCase) deleteWarnedAccounts(ctx context.Context, warnedBefore time.Time) (deleted, kept int64, err error) {
	for {
		accounts, err := u.store.ListInactiveAccountsToDelete(ctx, warnedBefore, u.policy.BatchSize)
		if err != nil {
			return deleted, kept, err
		}
		for _, account := range accounts {
			removed, err := u.accounts.DeleteInactiveAccount(ctx, account.UserID, account.WarnedAt)
			if err != nil {
				return deleted, kept, err
			}
			if removed {
				deleted++
			} else {
				kept++
			}
		}
		if len(accounts) < int(u.policy.BatchSize) {
			return deleted, kept, nil
		}
	}
}

// drain repeats one batched purge until a batch comes back short, so every
// statement holds its locks briefly while the run still clears the backlog.
func drain(ctx context.Context, batchSize int32, purge func(context.Context) (int64, error)) (int64, error) {
	var total int64
	for {
		n, err := purge(ctx)
		total += n
		if err != nil || n < int64(batchSize) {
			return total, err
		}
		if err = ctx.Err(); err != nil {
			return total, err
		}
	}
}
