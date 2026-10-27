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
	PurgeRolledUpEventLabObservations(context.Context, postgres.PurgeRolledUpEventLabObservationsParams) (int64, error)
	PurgePlatformLabCapacityObservations(context.Context, postgres.PurgePlatformLabCapacityObservationsParams) (int64, error)
	PurgeNotificationDispatches(context.Context, postgres.PurgeNotificationDispatchesParams) (int64, error)
	PurgeFinishedSignals(context.Context, postgres.PurgeFinishedSignalsParams) (int64, error)
	PurgeEventActivity(context.Context, postgres.PurgeEventActivityParams) (int64, error)
	PurgeEventStandTransitions(context.Context, postgres.PurgeEventStandTransitionsParams) (int64, error)
	PurgeEventVPNSessions(context.Context, postgres.PurgeEventVPNSessionsParams) (int64, error)
	ListLabTelemetryErasures(context.Context, int32) ([]postgres.LabTelemetryErasure, error)
	ListLabArchiveChunksForScrub(context.Context, postgres.ListLabArchiveChunksForScrubParams) ([]postgres.ListLabArchiveChunksForScrubRow, error)
	UpdateLabArchiveChunkData(context.Context, postgres.UpdateLabArchiveChunkDataParams) error
	ScrubRawLabObservations(context.Context, postgres.ScrubRawLabObservationsParams) (int64, error)
	ScrubLabMonitoringCurrent(context.Context, postgres.ScrubLabMonitoringCurrentParams) (int64, error)
	RenameErasedLabClient(context.Context, postgres.RenameErasedLabClientParams) error
	MarkLabTelemetryErasuresScrubbed(context.Context, postgres.MarkLabTelemetryErasuresScrubbedParams) error
	DeleteSettledLabTelemetryErasures(context.Context, time.Time) (int64, error)
	PurgeEventLabUsageMinutes(context.Context, postgres.PurgeEventLabUsageMinutesParams) (int64, error)
	PurgeEventLabMinutes(context.Context, postgres.PurgeEventLabMinutesParams) (int64, error)
	PurgeEventVPNClientMinutes(context.Context, postgres.PurgeEventVPNClientMinutesParams) (int64, error)
	PurgeEventLabTransitions(context.Context, postgres.PurgeEventLabTransitionsParams) (int64, error)
	PurgeEventLabJournalStates(context.Context, postgres.PurgeEventLabJournalStatesParams) (int64, error)
	PurgeEventLabObservationArchives(context.Context, postgres.PurgeEventLabObservationArchivesParams) (int64, error)
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

// PurgeRolledUpEventLabObservations removes raw lab snapshots received before
// receivedBefore that the rollups have consumed, keeping the latest one of
// every lab group.
func (r *Repository) PurgeRolledUpEventLabObservations(
	ctx context.Context, receivedBefore time.Time, batchSize int32,
) (int64, error) {
	params := postgres.PurgeRolledUpEventLabObservationsParams{
		ReceivedBefore: receivedBefore, BatchSize: batchSize,
	}
	return r.q.PurgeRolledUpEventLabObservations(ctx, params)
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

// PurgeEventLabTelemetry removes the derived lab telemetry (minute tables,
// transitions journal and states, raw archive) of events whose access closed
// before closedBefore, and the rows of events with no end older than it: up
// to batchSize rows of each table per call. The sum reaches batchSize while
// either table may hold more, so the drain loop runs until all are clear.
func (r *Repository) PurgeEventLabTelemetry(ctx context.Context, closedBefore time.Time, batchSize int32) (int64, error) {
	var total int64
	for _, purge := range []func() (int64, error){
		func() (int64, error) {
			return r.q.PurgeEventLabUsageMinutes(ctx, postgres.PurgeEventLabUsageMinutesParams{ClosedBefore: closedBefore, BatchSize: batchSize})
		},
		func() (int64, error) {
			return r.q.PurgeEventLabMinutes(ctx, postgres.PurgeEventLabMinutesParams{ClosedBefore: closedBefore, BatchSize: batchSize})
		},
		func() (int64, error) {
			return r.q.PurgeEventVPNClientMinutes(ctx, postgres.PurgeEventVPNClientMinutesParams{ClosedBefore: closedBefore, BatchSize: batchSize})
		},
		func() (int64, error) {
			return r.q.PurgeEventLabTransitions(ctx, postgres.PurgeEventLabTransitionsParams{ClosedBefore: closedBefore, BatchSize: batchSize})
		},
		func() (int64, error) {
			return r.q.PurgeEventLabJournalStates(ctx, postgres.PurgeEventLabJournalStatesParams{ClosedBefore: closedBefore, BatchSize: batchSize})
		},
		func() (int64, error) {
			return r.q.PurgeEventLabObservationArchives(ctx, postgres.PurgeEventLabObservationArchivesParams{ClosedBefore: closedBefore, BatchSize: batchSize})
		},
	} {
		n, err := purge()
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
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

// LabTelemetryErasure is a deleted account whose lab telemetry still gets rewritten: ClientName becomes Pseudonym.
type LabTelemetryErasure struct {
	UserID             uuid.UUID
	ClientName         string
	Pseudonym          string
	EventIDs           []uuid.UUID
	QueuedAt           time.Time
	ArchivesScrubbedAt *time.Time
}

// LabArchiveChunk is an archive chunk to scrub.
type LabArchiveChunk struct {
	ID    uuid.UUID
	Codec string
	Data  []byte
}

// LabTelemetryErasures lists up to batchSize queued accounts, oldest first.
func (r *Repository) LabTelemetryErasures(ctx context.Context, batchSize int32) ([]LabTelemetryErasure, error) {
	rows, err := r.q.ListLabTelemetryErasures(ctx, batchSize)
	if err != nil {
		return nil, err
	}
	out := make([]LabTelemetryErasure, 0, len(rows))
	for _, row := range rows {
		e := LabTelemetryErasure{
			UserID: row.UserID, ClientName: row.ClientName, Pseudonym: row.Pseudonym, EventIDs: row.EventIds, QueuedAt: row.QueuedAt,
		}
		if row.ArchivesScrubbedAt.Valid {
			e.ArchivesScrubbedAt = &row.ArchivesScrubbedAt.Time
		}
		out = append(out, e)
	}
	return out, nil
}

// ScrubRawLabObservations rewrites up to batchSize raw observations of the events that carry the client name.
func (r *Repository) ScrubRawLabObservations(ctx context.Context, e LabTelemetryErasure, batchSize int32) (int64, error) {
	return r.q.ScrubRawLabObservations(ctx, postgres.ScrubRawLabObservationsParams{
		ClientName: e.ClientName, Pseudonym: e.Pseudonym, EventIds: e.EventIDs, BatchSize: batchSize,
	})
}

// ScrubLabMonitoringCurrent rewrites the current state of the events that carries the client name.
func (r *Repository) ScrubLabMonitoringCurrent(ctx context.Context, e LabTelemetryErasure) (int64, error) {
	return r.q.ScrubLabMonitoringCurrent(ctx, postgres.ScrubLabMonitoringCurrentParams{
		ClientName: e.ClientName, Pseudonym: e.Pseudonym, EventIds: e.EventIDs,
	})
}

// RenameErasedLabClient gives the rollup rows still named after the client its pseudonym.
func (r *Repository) RenameErasedLabClient(ctx context.Context, e LabTelemetryErasure) error {
	return r.q.RenameErasedLabClient(ctx, postgres.RenameErasedLabClientParams{ClientName: e.ClientName, Pseudonym: e.Pseudonym})
}

// LabArchiveChunksForScrub pages through an event's archive chunks created after createdAfter, by id.
func (r *Repository) LabArchiveChunksForScrub(
	ctx context.Context, eventID, afterID uuid.UUID, createdAfter time.Time, pageSize int32,
) ([]LabArchiveChunk, error) {
	rows, err := r.q.ListLabArchiveChunksForScrub(ctx, postgres.ListLabArchiveChunksForScrubParams{
		EventID: eventID, AfterID: afterID, CreatedAfter: createdAfter, PageSize: pageSize,
	})
	if err != nil {
		return nil, err
	}
	out := make([]LabArchiveChunk, 0, len(rows))
	for _, row := range rows {
		out = append(out, LabArchiveChunk{ID: row.ID, Codec: row.Codec, Data: row.Data})
	}
	return out, nil
}

// SaveScrubbedLabArchiveChunk replaces the data of a chunk.
func (r *Repository) SaveScrubbedLabArchiveChunk(ctx context.Context, id uuid.UUID, data []byte) error {
	return r.q.UpdateLabArchiveChunkData(ctx, postgres.UpdateLabArchiveChunkDataParams{ID: id, Data: data})
}

// MarkLabTelemetryErasuresScrubbed records that the archive was scrubbed for the accounts up to scrubbedAt.
func (r *Repository) MarkLabTelemetryErasuresScrubbed(ctx context.Context, userIDs []uuid.UUID, scrubbedAt time.Time) error {
	return r.q.MarkLabTelemetryErasuresScrubbed(ctx, postgres.MarkLabTelemetryErasuresScrubbedParams{UserIds: userIDs, ScrubbedAt: scrubbedAt})
}

// DeleteSettledLabTelemetryErasures removes the entries queued before settledBefore whose events' raw rows of the
// first day are all archived.
func (r *Repository) DeleteSettledLabTelemetryErasures(ctx context.Context, settledBefore time.Time) (int64, error) {
	return r.q.DeleteSettledLabTelemetryErasures(ctx, settledBefore)
}
