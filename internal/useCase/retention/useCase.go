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

	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
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
		PurgeRolledUpEventLabObservations(ctx context.Context, receivedBefore time.Time, batchSize int32) (int64, error)
		LabTelemetryErasures(ctx context.Context, after retentionRepo.LabTelemetryErasure, batchSize int32) ([]retentionRepo.LabTelemetryErasure, error)
		SettledLabTelemetryErasures(ctx context.Context, settledBefore, forceBefore time.Time) ([]retentionRepo.LabTelemetryErasure, error)
		LabTelemetryErasuresQueuedBefore(ctx context.Context, before time.Time) (int64, error)
		DeleteLabTelemetryErasures(ctx context.Context, entries []retentionRepo.LabTelemetryErasure) error
		QueueLabTelemetryErasure(ctx context.Context, userID uuid.UUID) (retentionRepo.LabTelemetryErasure, error)
		ScrubRawLabObservations(ctx context.Context, e retentionRepo.LabTelemetryErasure, batchSize int32) (int64, error)
		ScrubLabMonitoringCurrent(ctx context.Context, e retentionRepo.LabTelemetryErasure) (int64, error)
		RenameErasedLabClient(ctx context.Context, e retentionRepo.LabTelemetryErasure) error
		LabArchiveChunksForScrub(ctx context.Context, eventID, afterID uuid.UUID, pageSize int32) ([]retentionRepo.LabArchiveChunk, error)
		SaveScrubbedLabArchiveChunk(ctx context.Context, id uuid.UUID, data []byte) error
		PurgeEventLabTelemetry(ctx context.Context, closedBefore, participantsBefore time.Time, batchSize int32) (int64, error)
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
		PurgeExpiredTemporalCodes(ctx context.Context, expiredBefore time.Time, batchSize int32) (int64, error)
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

	// Policies give the retention policy in force (satisfied by *platformSettingsUseCase.PlatformSettingsUseCase):
	// the periods saved in the platform settings over the env defaults.
	Policies interface {
		RetentionPolicy(ctx context.Context) (retentionModel.Policy, error)
	}

	Dependencies struct {
		Store    Store
		Notifier Notifier
		Accounts AccountRemover
		Policies Policies
		// SignInURL is the ID sign-in page linked from the warning email.
		SignInURL string
		// Now is the clock; nil means time.Now.
		Now func() time.Time
	}

	RetentionUseCase struct {
		store     Store
		notifier  Notifier
		accounts  AccountRemover
		policies  Policies
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
		policies: deps.Policies, signInURL: deps.SignInURL, now: now,
	}
}

// EnforceDataRetention purges every kind of data past its period, and the
// residual personal data of deleted accounts. A failing step does not stop
// the others; the run logs one line with the counts and returns the failures. The policy is read once at the start
// and holds for the whole run; without it nothing is purged.
func (u *RetentionUseCase) EnforceDataRetention(ctx context.Context) error {
	policy, err := u.policies.RetentionPolicy(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the retention policy").Err()
	}
	started := u.now()
	cut := policy.Cutoffs(started)
	batch := policy.BatchSize
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
		// The raw rows hold clients and IPs: the participants period.
		return u.store.PurgeEventLabObservations(ctx, cut.LabParticipantsBefore, batch)
	})
	rolledUpObservations := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeRolledUpEventLabObservations(ctx, cut.LabRolledUpBefore, batch)
	})
	// The rollups, journal and archive derived from them wait for the event's access to close.
	labTelemetry := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeEventLabTelemetry(ctx, cut.LabObservedBefore, cut.LabParticipantsBefore, batch)
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
	// The archived raw telemetry of the accounts just purged: their VPN client names and IPs go.
	scrubbedChunks, err := u.scrubLabTelemetry(ctx, batch)
	if err != nil {
		errs = append(errs, err)
	}
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

	// One-time codes are useless after their expiry (an email change code holds an address).
	temporalCodes := step(func(ctx context.Context) (int64, error) {
		return u.store.PurgeExpiredTemporalCodes(ctx, started, batch)
	})

	err = errors.Join(errs...)
	log.Info().Err(err).
		Int64("sessions", sessions).
		Int64("lab_observations", labObservations).
		Int64("lab_observations_rolled_up", rolledUpObservations).
		Int64("lab_telemetry", labTelemetry).
		Int64("lab_capacity_observations", capacityObservations).
		Int64("notification_dispatches", dispatches).
		Int64("signals", signals).
		Int64("event_analytics", analytics).
		Int64("form_answers", formAnswers).
		Int64("answer_files", answerFiles).
		Int64("deleted_accounts_purged", deletedAccounts).
		Int64("lab_archive_chunks_scrubbed", scrubbedChunks).
		Int64("expired_invitations", invitations).
		Int64("unconfirmed_accounts", unconfirmedAccounts).
		Int64("expired_temporal_codes", temporalCodes).
		Dur("took", u.now().Sub(started)).
		Msg("data retention: purge run")
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to enforce data retention").Err()
	}
	return nil
}

// labScrubPageSize archive chunks are unpacked at a time.
const labScrubPageSize = 5

const (
	// labErasureSettleAfter is how long an erased person stays queued: the lab drops a deleted account's VPN client
	// within minutes and the archive follows the raw rows by about an hour, so after a day nothing can bring the
	// real name back.
	labErasureSettleAfter = 24 * time.Hour
	// labErasureForceAfter closes an entry whatever the archive says (an event that is never archived), after
	// reporting it: the link between the person and the pseudonym must not live on.
	labErasureForceAfter = 7 * 24 * time.Hour
)

// scrubLabTelemetry removes the link to erased people from the lab telemetry that still carries it: the raw
// observations and the current state (rewritten at once when the account is purged, and again here for what
// arrived since), the rollup rows built from them and every archive chunk of the events they took part in. The VPN
// client name becomes the person's pseudonym everywhere; IPs and counters stay. Every step is idempotent. Entries
// close a day after they were queued, once the archive passed the raw rows of that day; the set to close is fixed
// before the pass so a chunk archived meanwhile cannot slip past it. The queue is read page by page whole.
// Returns the archive chunks it rewrote.
func (u *RetentionUseCase) scrubLabTelemetry(ctx context.Context, batch int32) (int64, error) {
	started := u.now()
	settled, err := u.store.SettledLabTelemetryErasures(ctx, started.Add(-labErasureSettleAfter), started.Add(-labErasureForceAfter))
	if err != nil {
		return 0, err
	}
	var rewritten int64
	var after retentionRepo.LabTelemetryErasure
	for {
		page, err := u.store.LabTelemetryErasures(ctx, after, batch)
		if err != nil {
			return rewritten, err
		}
		if len(page) == 0 {
			break
		}
		n, err := u.scrubErasures(ctx, page, batch)
		rewritten += n
		if err != nil {
			return rewritten, err
		}
		after = page[len(page)-1]
		if len(page) < int(batch) {
			break
		}
	}
	if len(settled) > 0 {
		if err = u.store.DeleteLabTelemetryErasures(ctx, settled); err != nil {
			return rewritten, err
		}
	}
	if stale, cerr := u.store.LabTelemetryErasuresQueuedBefore(ctx, started.Add(-labErasureSettleAfter)); cerr != nil {
		return rewritten, cerr
	} else if stale > 0 {
		log.Error().Int64("entries", stale).Msg("data retention: lab telemetry erasures not settled after a day")
	}
	return rewritten, nil
}

// scrubErasures rewrites the live data of a page of erased people and every archive chunk of their events.
func (u *RetentionUseCase) scrubErasures(ctx context.Context, page []retentionRepo.LabTelemetryErasure, batch int32) (int64, error) {
	erasures := map[string]eventAnalyticsModel.LabArchiveErasure{}
	events := map[uuid.UUID]struct{}{}
	for _, e := range page {
		if err := u.scrubLive(ctx, e, batch); err != nil {
			return 0, err
		}
		erasures[e.ClientName] = eventAnalyticsModel.LabArchiveErasure{Pseudonym: e.Pseudonym, EraseIP: e.EraseIP}
		for _, id := range e.EventIDs {
			events[id] = struct{}{}
		}
	}
	var rewritten int64
	for eventID := range events {
		n, err := u.scrubEventArchive(ctx, eventID, erasures)
		rewritten += n
		if err != nil {
			return rewritten, err
		}
	}
	return rewritten, nil
}

// scrubLive rewrites the raw observations and the current state of one erased account and renames the rollup rows.
func (u *RetentionUseCase) scrubLive(ctx context.Context, e retentionRepo.LabTelemetryErasure, batch int32) error {
	if len(e.EventIDs) > 0 {
		if _, err := drain(ctx, batch, func(ctx context.Context) (int64, error) {
			return u.store.ScrubRawLabObservations(ctx, e, batch)
		}); err != nil {
			return err
		}
		if _, err := u.store.ScrubLabMonitoringCurrent(ctx, e); err != nil {
			return err
		}
	}
	return u.store.RenameErasedLabClient(ctx, e)
}

func (u *RetentionUseCase) scrubEventArchive(ctx context.Context, eventID uuid.UUID, erasures map[string]eventAnalyticsModel.LabArchiveErasure) (int64, error) {
	var rewritten int64
	after := uuid.Nil
	for {
		chunks, err := u.store.LabArchiveChunksForScrub(ctx, eventID, after, labScrubPageSize)
		if err != nil {
			return rewritten, err
		}
		for _, chunk := range chunks {
			after = chunk.ID
			data, changed, err := eventAnalyticsModel.ScrubLabArchive(chunk.Codec, chunk.Data, erasures)
			if err != nil {
				return rewritten, err
			}
			if !changed {
				continue
			}
			if err = u.store.SaveScrubbedLabArchiveChunk(ctx, chunk.ID, data); err != nil {
				return rewritten, err
			}
			rewritten++
		}
		if len(chunks) < labScrubPageSize {
			return rewritten, nil
		}
	}
}

// EraseLabTelemetryOf erases one person from the lab telemetry on request (an objection), whether or not the
// account exists: the VPN client name becomes a random pseudonym in every table, raw rows, current state and
// archive included; IPs and counters stay. The person's VPN client keeps its name while it lives, so what the labs
// report under it until the entry settles (a day) is rewritten by the retention job; run it after the access ended
// to be done for good. Returns the entry and the archive chunks rewritten.
func (u *RetentionUseCase) EraseLabTelemetryOf(ctx context.Context, userID uuid.UUID) (retentionRepo.LabTelemetryErasure, int64, error) {
	entry, err := u.store.QueueLabTelemetryErasure(ctx, userID)
	if err != nil {
		return entry, 0, model.ErrPlatform.WithError(err).WithMessage("Failed to queue the erasure").Err()
	}
	batch := retentionModel.DefaultBatchSize
	if u.policies != nil {
		if policy, perr := u.policies.RetentionPolicy(ctx); perr == nil {
			batch = int(policy.BatchSize)
		}
	}
	rewritten, err := u.scrubErasures(ctx, []retentionRepo.LabTelemetryErasure{entry}, int32(batch)) //nolint:gosec // a batch size
	if err != nil {
		return entry, rewritten, model.ErrPlatform.WithError(err).WithMessage("Failed to erase the person from the lab telemetry").Err()
	}
	return entry, rewritten, nil
}

// EnforceAccountInactivity warns the owners of accounts inactive for the
// policy period and deletes the accounts still inactive a grace period after
// the warning. Signing in again voids the warning. The deletion goes through
// the deletion-request cascade; the next purge run removes the residual
// personal data and keeps competition results under the anonymized tombstone. The policy is read once at the start
// and holds for the whole run; without it nothing runs.
func (u *RetentionUseCase) EnforceAccountInactivity(ctx context.Context) error {
	policy, err := u.policies.RetentionPolicy(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the retention policy").Err()
	}
	started := u.now()
	cut := policy.Cutoffs(started)

	cleared, err := u.store.ClearReturnedInactivityWarnings(ctx)
	var warned, deleted, kept int64
	if err == nil {
		warned, err = u.warnInactiveAccounts(ctx, policy, started, cut.InactiveSince)
	}
	if err == nil {
		deleted, kept, err = u.deleteWarnedAccounts(ctx, policy, cut.WarnedBefore)
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
func (u *RetentionUseCase) warnInactiveAccounts(ctx context.Context, policy retentionModel.Policy, now, inactiveSince time.Time) (int64, error) {
	deletionDate := policy.DeletionDate(now).Format(deletionDateLayout)
	var warned int64
	for {
		accounts, err := u.store.ListInactiveAccountsToWarn(ctx, inactiveSince, policy.BatchSize)
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
		if len(accounts) < int(policy.BatchSize) {
			return warned, nil
		}
	}
}

// deleteWarnedAccounts deletes the accounts whose grace period is over. An
// account whose owner signed in meanwhile is kept (and no longer listed).
func (u *RetentionUseCase) deleteWarnedAccounts(ctx context.Context, policy retentionModel.Policy, warnedBefore time.Time) (deleted, kept int64, err error) {
	for {
		accounts, err := u.store.ListInactiveAccountsToDelete(ctx, warnedBefore, policy.BatchSize)
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
		if len(accounts) < int(policy.BatchSize) {
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
