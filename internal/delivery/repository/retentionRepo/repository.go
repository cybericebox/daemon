// Package retentionRepo runs the data retention statements (Privacy Policy,
// "Retention"). Every method is a set operation over at most batchSize rows,
// not an aggregate mutation: the purges delete rows past their period, and
// the inactivity-warning marks are narrow writes to users.inactivity_warned_at,
// a column outside the user aggregate's UPDATE set (like last_seen).
package retentionRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
)

type Queries interface {
	PurgeExpiredSessions(context.Context, postgres.PurgeExpiredSessionsParams) (int64, error)
	PurgeEventLabObservations(context.Context, postgres.PurgeEventLabObservationsParams) (int64, error)
	PurgePlatformLabCapacityObservations(context.Context, postgres.PurgePlatformLabCapacityObservationsParams) (int64, error)
	PurgeNotificationDispatches(context.Context, postgres.PurgeNotificationDispatchesParams) (int64, error)
	PurgeFinishedSignals(context.Context, postgres.PurgeFinishedSignalsParams) (int64, error)
	PurgeEventActivity(context.Context, postgres.PurgeEventActivityParams) (int64, error)
	PurgeEventStandTransitions(context.Context, postgres.PurgeEventStandTransitionsParams) (int64, error)
	PurgeEventVPNSessions(context.Context, postgres.PurgeEventVPNSessionsParams) (int64, error)
	PurgeEventLabTouches(context.Context, postgres.PurgeEventLabTouchesParams) (int64, error)
	PurgeEventLabTrafficCoverage(context.Context, postgres.PurgeEventLabTrafficCoverageParams) (int64, error)
	PurgeEventFormAnswers(context.Context, postgres.PurgeEventFormAnswersParams) (int64, error)
	PurgeEventAnswerFiles(context.Context, postgres.PurgeEventAnswerFilesParams) (int64, error)
	PurgeDeletedAccountsPersonalData(context.Context, postgres.PurgeDeletedAccountsPersonalDataParams) (int64, error)
	ClearReturnedInactivityWarnings(context.Context) (int64, error)
	ListInactiveAccountsToWarn(context.Context, postgres.ListInactiveAccountsToWarnParams) ([]postgres.ListInactiveAccountsToWarnRow, error)
	MarkInactivityWarned(context.Context, postgres.MarkInactivityWarnedParams) (int64, error)
	ListInactiveAccountsToDelete(context.Context, postgres.ListInactiveAccountsToDeleteParams) ([]postgres.ListInactiveAccountsToDeleteRow, error)
	PurgeExpiredEventInvitations(context.Context, postgres.PurgeExpiredEventInvitationsParams) (int64, error)
	PurgeExpiredTemporalCodes(context.Context, postgres.PurgeExpiredTemporalCodesParams) (int64, error)
	PurgeUnconfirmedAccounts(context.Context, postgres.PurgeUnconfirmedAccountsParams) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) PurgeExpiredSessions(ctx context.Context, expiredBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeExpiredSessions(ctx, postgres.PurgeExpiredSessionsParams{ExpiredBefore: expiredBefore, BatchSize: batchSize})
}

func (r *Repository) PurgeEventLabObservations(ctx context.Context, observedBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeEventLabObservations(ctx, postgres.PurgeEventLabObservationsParams{ObservedBefore: observedBefore, BatchSize: batchSize})
}

func (r *Repository) PurgePlatformLabCapacityObservations(ctx context.Context, observedBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgePlatformLabCapacityObservations(ctx, postgres.PurgePlatformLabCapacityObservationsParams{ObservedBefore: observedBefore, BatchSize: batchSize})
}

func (r *Repository) PurgeNotificationDispatches(ctx context.Context, createdBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeNotificationDispatches(ctx, postgres.PurgeNotificationDispatchesParams{CreatedBefore: createdBefore, BatchSize: batchSize})
}

// PurgeFinishedSignals removes signals finished and created before
// createdBefore, with their hook executions.
func (r *Repository) PurgeFinishedSignals(ctx context.Context, createdBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeFinishedSignals(ctx, postgres.PurgeFinishedSignalsParams{CreatedBefore: createdBefore, BatchSize: batchSize})
}

// PurgeEventAnalytics removes the analytics logs (activity, stand
// transitions, VPN sessions, lab access aggregates and their coverage) of events that ended before endedBefore: up to batchSize rows
// of each log per call. The sum reaches batchSize while either log may hold
// more, so the drain loop runs until both are clear.
func (r *Repository) PurgeEventAnalytics(ctx context.Context, endedBefore time.Time, batchSize int32) (int64, error) {
	activity, err := r.q.PurgeEventActivity(ctx, postgres.PurgeEventActivityParams{EndedBefore: endedBefore, BatchSize: batchSize})
	if err != nil {
		return activity, err
	}
	transitions, err := r.q.PurgeEventStandTransitions(ctx, postgres.PurgeEventStandTransitionsParams{EndedBefore: endedBefore, BatchSize: batchSize})
	if err != nil {
		return activity + transitions, err
	}
	sessions, err := r.q.PurgeEventVPNSessions(ctx, postgres.PurgeEventVPNSessionsParams{EndedBefore: endedBefore, BatchSize: batchSize})
	if err != nil {
		return activity + transitions + sessions, err
	}
	touches, err := r.q.PurgeEventLabTouches(ctx, postgres.PurgeEventLabTouchesParams{EndedBefore: endedBefore, BatchSize: batchSize})
	if err != nil {
		return activity + transitions + sessions + touches, err
	}
	coverage, err := r.q.PurgeEventLabTrafficCoverage(ctx, postgres.PurgeEventLabTrafficCoverageParams{EndedBefore: endedBefore, BatchSize: batchSize})
	total := activity + transitions + sessions + touches + coverage
	return total, err
}

func (r *Repository) PurgeEventFormAnswers(ctx context.Context, endedBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeEventFormAnswers(ctx, postgres.PurgeEventFormAnswersParams{EndedBefore: endedBefore, BatchSize: batchSize})
}

// PurgeEventAnswerFiles releases the answer files of events that ended
// before endedBefore and the uploads never used in an answer.
func (r *Repository) PurgeEventAnswerFiles(ctx context.Context, endedBefore, unusedBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeEventAnswerFiles(ctx, postgres.PurgeEventAnswerFilesParams{EndedBefore: endedBefore, PendingBefore: unusedBefore, BatchSize: batchSize})
}

// PurgeDeletedAccounts removes the residual personal data of up to batchSize
// deleted accounts and returns how many accounts it processed.
func (r *Repository) PurgeDeletedAccounts(ctx context.Context, now time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeDeletedAccountsPersonalData(ctx, postgres.PurgeDeletedAccountsPersonalDataParams{PurgedAt: now, BatchSize: batchSize})
}

func (r *Repository) ClearReturnedInactivityWarnings(ctx context.Context) (int64, error) {
	return r.q.ClearReturnedInactivityWarnings(ctx)
}

func (r *Repository) ListInactiveAccountsToWarn(ctx context.Context, inactiveSince time.Time, batchSize int32) ([]retentionModel.InactiveAccount, error) {
	rows, err := r.q.ListInactiveAccountsToWarn(ctx, postgres.ListInactiveAccountsToWarnParams{InactiveSince: inactiveSince, BatchSize: batchSize})
	if err != nil {
		return nil, err
	}
	out := make([]retentionModel.InactiveAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, retentionModel.InactiveAccount{UserID: row.ID, FirstName: row.FirstName, LastSeen: row.LastSeen})
	}
	return out, nil
}

// MarkInactivityWarned records the warning; false when the account was
// already warned or deleted meanwhile.
func (r *Repository) MarkInactivityWarned(ctx context.Context, userID uuid.UUID, warnedAt time.Time) (bool, error) {
	n, err := r.q.MarkInactivityWarned(ctx, postgres.MarkInactivityWarnedParams{ID: userID, WarnedAt: warnedAt})
	return n == 1, err
}

func (r *Repository) ListInactiveAccountsToDelete(ctx context.Context, warnedBefore time.Time, batchSize int32) ([]retentionModel.WarnedAccount, error) {
	rows, err := r.q.ListInactiveAccountsToDelete(ctx, postgres.ListInactiveAccountsToDeleteParams{WarnedBefore: warnedBefore, BatchSize: batchSize})
	if err != nil {
		return nil, err
	}
	out := make([]retentionModel.WarnedAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, retentionModel.WarnedAccount{UserID: row.ID, WarnedAt: row.WarnedAt})
	}
	return out, nil
}

// PurgeExpiredEventInvitations removes pending invitations that can no longer
// be accepted since before expiredBefore.
func (r *Repository) PurgeExpiredEventInvitations(ctx context.Context, expiredBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeExpiredEventInvitations(ctx, postgres.PurgeExpiredEventInvitationsParams{ExpiredBefore: expiredBefore, BatchSize: batchSize})
}

// PurgeExpiredTemporalCodes removes up to batchSize one-time codes (reset, confirmation, email change, setup
// links) that expired before expiredBefore.
func (r *Repository) PurgeExpiredTemporalCodes(ctx context.Context, expiredBefore time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeExpiredTemporalCodes(ctx, postgres.PurgeExpiredTemporalCodesParams{ExpiredBefore: expiredBefore, BatchSize: batchSize})
}

// PurgeUnconfirmedAccounts removes up to batchSize accounts whose
// registration was never finished, created before createdBefore and holding
// no invitation live at now; their teams are handed over or removed in the
// same statement. It returns how many accounts it removed.
func (r *Repository) PurgeUnconfirmedAccounts(ctx context.Context, createdBefore, now time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeUnconfirmedAccounts(ctx, postgres.PurgeUnconfirmedAccountsParams{CreatedBefore: createdBefore, NowAt: now, BatchSize: batchSize})
}
