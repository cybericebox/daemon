// Package eventAnalyticsRepo holds the query-side statements of event
// analytics: the rollup job's set operations (5-minute buckets, VPN
// sessions, progress state) and the report reads. None of it is an
// aggregate; every write is an idempotent rebuild from the sources.
package eventAnalyticsRepo

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

type Queries interface {
	StandsQueries
	UsageQueries
	IntegrityQueries
	ReportQueries
	ParticipantQueries
	ListEventAnalyticsRollupCandidates(context.Context, postgres.ListEventAnalyticsRollupCandidatesParams) ([]postgres.ListEventAnalyticsRollupCandidatesRow, error)
	RefreshEventActivityBuckets(context.Context, uuid.UUID) error
	MarkEventActivityBucketsRefreshed(context.Context, postgres.MarkEventActivityBucketsRefreshedParams) error
	ListEventVPNSamples(context.Context, postgres.ListEventVPNSamplesParams) ([]postgres.ListEventVPNSamplesRow, error)
	ListOpenEventVPNSessions(context.Context, postgres.ListOpenEventVPNSessionsParams) ([]postgres.EventVpnSession, error)
	UpsertEventVPNSession(context.Context, postgres.UpsertEventVPNSessionParams) error
	DeleteEventVPNSessions(context.Context, postgres.DeleteEventVPNSessionsParams) error
	SetEventVPNCursor(context.Context, postgres.SetEventVPNCursorParams) error
	GetEventAnalyticsOverview(context.Context, postgres.GetEventAnalyticsOverviewParams) (postgres.GetEventAnalyticsOverviewRow, error)
	ListEventActivitySeries(context.Context, postgres.ListEventActivitySeriesParams) ([]postgres.ListEventActivitySeriesRow, error)
	GetEventAnalyticsRollupState(context.Context, uuid.UUID) (postgres.GetEventAnalyticsRollupStateRow, error)
	TaskQueries
	ListEventAnalyticsFeed(context.Context, postgres.ListEventAnalyticsFeedParams) ([]postgres.ListEventAnalyticsFeedRow, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

type (
	// RollupCandidate is an event the rollup job still has work for.
	RollupCandidate struct {
		EventID               uuid.UUID
		StartAt               time.Time
		FinishedAt            *time.Time
		InfrastructureAllowed bool
		// Revision is the current results revision.
		Revision int64
		// BucketsRevision is the results revision of the last rebuild.
		BucketsRevision  int64
		BucketsFinalized bool
		VPNFinalized     bool
		VPNCursor        VPNCursor
	}

	// VPNCursor is the last observation the VPN rollup read (arrival order);
	// the zero cursor reads from the beginning.
	VPNCursor struct {
		ReceivedAt time.Time
		ID         uuid.UUID
	}

	// Overview is the §6.1 counter set.
	Overview struct {
		ParticipantsRegistered, ParticipantsApproved, ParticipantsPending, ParticipantsInvited, ParticipantsActive int64
		TeamsTotal, TeamsAdmitted                                                                                  int64
		Attempts, AttemptsCorrect, Solves                                                                          int64
		HintsOpened, HintPoints                                                                                    int64
		StandsCreating, StandsReady, StandsFailed                                                                  int64
	}

	// SeriesPoint is one 5-minute bucket of the event-wide series.
	SeriesPoint struct {
		At                               time.Time
		Attempts, Correct, Solves, Opens int64
	}

	// FeedItem is one notable moment of an event (§6.1). Kind is
	// first_blood, stand_failed or team_created; Challenge is empty for
	// team_created, Detail carries the failure reason of a stand.
	FeedItem struct {
		Kind          string
		At            time.Time
		TeamID        uuid.UUID
		TeamName      string
		ChallengeID   uuid.UUID
		ChallengeName string
		Detail        string
	}

	// RollupState is when the event's buckets were last rebuilt and whether
	// they are final.
	RollupState struct {
		RefreshedAt *time.Time
		FinalizedAt *time.Time
	}
)

// cursorStart precedes every observation (a zero time is year 1).
var cursorStart = time.Unix(0, 0).UTC()

func (r *Repository) RollupCandidates(ctx context.Context, now time.Time, limit int32) ([]RollupCandidate, error) {
	rows, err := r.q.ListEventAnalyticsRollupCandidates(ctx, postgres.ListEventAnalyticsRollupCandidatesParams{Now: now, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]RollupCandidate, 0, len(rows))
	for _, row := range rows {
		c := RollupCandidate{
			EventID: row.ID, StartAt: row.StartAt, InfrastructureAllowed: row.InfrastructureAllowed, Revision: row.Revision,
			BucketsRevision:  row.BucketsRevision,
			BucketsFinalized: row.BucketsFinalizedAt.Valid, VPNFinalized: row.VpnFinalizedAt.Valid,
		}
		if row.HasFinish {
			finish := row.FinishedAt
			c.FinishedAt = &finish
		}
		if row.VpnCursorReceivedAt.Valid && row.VpnCursorID.Valid {
			c.VPNCursor = VPNCursor{ReceivedAt: row.VpnCursorReceivedAt.Time, ID: row.VpnCursorID.UUID}
		}
		out = append(out, c)
	}
	return out, nil
}

// RefreshBuckets rebuilds the event's 5-minute buckets from the sources.
func (r *Repository) RefreshBuckets(ctx context.Context, eventID uuid.UUID) error {
	return r.q.RefreshEventActivityBuckets(ctx, eventID)
}

// MarkBucketsRefreshed records a rebuild at the given results revision;
// finalizedAt is set once the event's buckets are final.
func (r *Repository) MarkBucketsRefreshed(ctx context.Context, eventID uuid.UUID, at time.Time, revision int64, finalizedAt *time.Time) error {
	return r.q.MarkEventActivityBucketsRefreshed(ctx, postgres.MarkEventActivityBucketsRefreshedParams{
		EventID: eventID, RefreshedAt: timestamptz(&at), Revision: revision, FinalizedAt: timestamptz(finalizedAt),
	})
}

// VPNSamples reads the peer statistics of up to batchSize observations after
// the cursor. It returns the samples, the cursor after the last observation
// read and how many observations it read (fewer than batchSize: caught up).
func (r *Repository) VPNSamples(ctx context.Context, eventID uuid.UUID, after VPNCursor, batchSize int32) ([]eventAnalyticsModel.VPNSample, VPNCursor, int, error) {
	if after.ReceivedAt.IsZero() {
		after.ReceivedAt = cursorStart
	}
	rows, err := r.q.ListEventVPNSamples(ctx, postgres.ListEventVPNSamplesParams{
		EventID: eventID, AfterReceivedAt: after.ReceivedAt, AfterID: after.ID, BatchSize: batchSize,
	})
	if err != nil {
		return nil, after, 0, err
	}
	samples := make([]eventAnalyticsModel.VPNSample, 0, len(rows))
	observations := 0
	next := after
	for _, row := range rows {
		if row.ObservationID != next.ID || !row.ReceivedAt.Equal(next.ReceivedAt) {
			observations++
			next = VPNCursor{ReceivedAt: row.ReceivedAt, ID: row.ObservationID}
		}
		if row.ClientName == "" || row.HandshakeUnix <= 0 {
			continue
		}
		samples = append(samples, eventAnalyticsModel.VPNSample{
			TeamID: row.TeamID, Client: row.ClientName, ObservedAt: row.ObservedAt,
			Handshake: time.Unix(row.HandshakeUnix, 0).UTC(), Rx: row.RxBytes, Tx: row.TxBytes,
		})
	}
	return samples, next, observations, nil
}

// OpenVPNSessions returns the event's sessions ending at or after endedAfter.
func (r *Repository) OpenVPNSessions(ctx context.Context, eventID uuid.UUID, endedAfter time.Time) ([]eventAnalyticsModel.VPNSession, error) {
	rows, err := r.q.ListOpenEventVPNSessions(ctx, postgres.ListOpenEventVPNSessionsParams{EventID: eventID, EndedAfter: endedAfter})
	if err != nil {
		return nil, err
	}
	out := make([]eventAnalyticsModel.VPNSession, 0, len(rows))
	for _, row := range rows {
		s := eventAnalyticsModel.VPNSession{
			ID: row.ID, TeamID: row.TeamID, Client: row.ClientName, StartedAt: row.StartedAt, EndedAt: row.EndedAt,
			RxMin: row.RxMin, RxMax: row.RxMax, TxMin: row.TxMin, TxMax: row.TxMax,
		}
		if row.UserID.Valid {
			userID := row.UserID.UUID
			s.UserID = &userID
		}
		out = append(out, s)
	}
	return out, nil
}

// SaveVPNSessions stores the changed sessions, then removes the absorbed
// ones. The order keeps a partial failure harmless: the next pass re-reads
// the same observations and joins any leftover duplicate.
func (r *Repository) SaveVPNSessions(ctx context.Context, eventID uuid.UUID, changed []eventAnalyticsModel.VPNSession, absorbed []uuid.UUID) error {
	for _, s := range changed {
		userID := uuid.NullUUID{}
		if s.UserID != nil {
			userID = uuid.NullUUID{UUID: *s.UserID, Valid: true}
		}
		if err := r.q.UpsertEventVPNSession(ctx, postgres.UpsertEventVPNSessionParams{
			ID: s.ID, EventID: eventID, TeamID: s.TeamID, UserID: userID, ClientName: s.Client,
			StartedAt: s.StartedAt, EndedAt: s.EndedAt, RxMin: s.RxMin, RxMax: s.RxMax, TxMin: s.TxMin, TxMax: s.TxMax,
		}); err != nil {
			return err
		}
	}
	if len(absorbed) == 0 {
		return nil
	}
	return r.q.DeleteEventVPNSessions(ctx, postgres.DeleteEventVPNSessionsParams{EventID: eventID, Ids: absorbed})
}

// SetVPNCursor records the VPN rollup progress; finalizedAt closes it.
func (r *Repository) SetVPNCursor(ctx context.Context, eventID uuid.UUID, cursor VPNCursor, finalizedAt *time.Time) error {
	params := postgres.SetEventVPNCursorParams{EventID: eventID, FinalizedAt: timestamptz(finalizedAt)}
	if cursor.ID != uuid.Nil {
		params.CursorReceivedAt = timestamptz(&cursor.ReceivedAt)
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	return r.q.SetEventVPNCursor(ctx, params)
}

// Overview reads the §6.1 counters; active counts actions since activeSince.
func (r *Repository) Overview(ctx context.Context, eventID uuid.UUID, activeSince time.Time) (Overview, error) {
	row, err := r.q.GetEventAnalyticsOverview(ctx, postgres.GetEventAnalyticsOverviewParams{EventID: eventID, ActiveSince: activeSince})
	if err != nil {
		return Overview{}, err
	}
	return Overview{
		ParticipantsRegistered: row.ParticipantsRegistered, ParticipantsApproved: row.ParticipantsApproved,
		ParticipantsPending: row.ParticipantsPending, ParticipantsInvited: row.ParticipantsInvited, ParticipantsActive: row.ParticipantsActive,
		TeamsTotal: row.TeamsTotal, TeamsAdmitted: row.TeamsAdmitted,
		Attempts: row.Attempts, AttemptsCorrect: row.AttemptsCorrect, Solves: row.Solves,
		HintsOpened: row.HintsOpened, HintPoints: row.HintPoints,
		StandsCreating: row.StandsCreating, StandsReady: row.StandsReady, StandsFailed: row.StandsFailed,
	}, nil
}

// Series reads the event-wide 5-minute series in the period (only buckets
// with activity).
func (r *Repository) Series(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]SeriesPoint, error) {
	rows, err := r.q.ListEventActivitySeries(ctx, postgres.ListEventActivitySeriesParams{EventID: eventID, FromAt: period.From, ToAt: period.To})
	if err != nil {
		return nil, err
	}
	out := make([]SeriesPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, SeriesPoint{At: row.BucketAt, Attempts: row.Attempts, Correct: row.Correct, Solves: row.Solves, Opens: row.Opens})
	}
	return out, nil
}

// Feed reads the newest notable moments of an event, newest first.
func (r *Repository) Feed(ctx context.Context, eventID uuid.UUID, limit int32) ([]FeedItem, error) {
	rows, err := r.q.ListEventAnalyticsFeed(ctx, postgres.ListEventAnalyticsFeedParams{EventID: eventID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]FeedItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, FeedItem{
			Kind: row.Kind, At: row.OccurredAt, TeamID: row.TeamID, TeamName: row.TeamName,
			ChallengeID: row.ChallengeID, ChallengeName: row.ChallengeName, Detail: row.Detail,
		})
	}
	return out, nil
}

// RollupState reads the buckets' progress; an event never rolled up has none.
func (r *Repository) RollupState(ctx context.Context, eventID uuid.UUID) (RollupState, error) {
	row, err := r.q.GetEventAnalyticsRollupState(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RollupState{}, nil
	}
	if err != nil {
		return RollupState{}, err
	}
	return RollupState{RefreshedAt: timePtr(row.BucketsRefreshedAt), FinalizedAt: timePtr(row.BucketsFinalizedAt)}, nil
}

func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
