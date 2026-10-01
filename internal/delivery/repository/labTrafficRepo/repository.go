// Package labTrafficRepo stores per-user lab access aggregates and the
// collector coverage (docs/LAB-TRAFFIC-ACCOUNTING.md). Every method is a set
// operation or a narrow read; the aggregate has no entity to load and mutate.
package labTrafficRepo

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
)

type Queries interface {
	ApplyLabTrafficTouch(context.Context, postgres.ApplyLabTrafficTouchParams) error
	ExtendLabTrafficCoverage(context.Context, postgres.ExtendLabTrafficCoverageParams) (int64, error)
	CreateLabTrafficCoverage(context.Context, postgres.CreateLabTrafficCoverageParams) error
	ListLabTrafficCoverage(context.Context, postgres.ListLabTrafficCoverageParams) ([]postgres.ListLabTrafficCoverageRow, error)
	SummarizeLabTouches(context.Context, postgres.SummarizeLabTouchesParams) ([]postgres.SummarizeLabTouchesRow, error)
	GetLabDeployedSince(context.Context, postgres.GetLabDeployedSinceParams) (time.Time, error)
	IsUserInEventTeam(context.Context, postgres.IsUserInEventTeamParams) (bool, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// zeroTime stands for NULL in the queries that return plain timestamps.
var zeroTime = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)

// IsUserInTeam reports whether the user is a participant of the event team.
func (r *Repository) IsUserInTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) (bool, error) {
	return r.q.IsUserInEventTeam(ctx, postgres.IsUserInEventTeamParams{EventID: eventID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, UserID: userID})
}

// ApplyTouch stores the collector's cumulative values for a user, lab and
// access type; totals never go down, first is min, last is max. An event
// deleted meanwhile is skipped, not an error.
func (r *Repository) ApplyTouch(ctx context.Context, touch labTraffic.Touch) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	params := postgres.ApplyLabTrafficTouchParams{
		ID: id, EventID: touch.EventID, TeamID: touch.TeamID, UserID: touch.UserID, EventChallengeID: touch.EventChallengeID,
		Surface: string(touch.Surface), AttemptsCount: touch.Attempts, FirstSeenAt: touch.FirstSeenAt, LastSeenAt: touch.LastSeenAt,
		PacketsOut: touch.PacketsOut, PacketsIn: touch.PacketsIn, BytesOut: touch.BytesOut, BytesIn: touch.BytesIn,
	}
	if touch.FirstRespondAt != nil {
		params.FirstRespondedAt = pgtype.Timestamptz{Time: *touch.FirstRespondAt, Valid: true}
	}
	err = r.q.ApplyLabTrafficTouch(ctx, params)
	if isForeignKeyViolation(err) {
		return nil
	}
	return err
}

// RecordCoverage extends the collector's latest segment when the span joins it,
// otherwise opens a new one.
func (r *Repository) RecordCoverage(ctx context.Context, eventID, teamID uuid.UUID, surface labTraffic.Surface, source, bootID string, span labTraffic.Coverage) error {
	extended, err := r.q.ExtendLabTrafficCoverage(ctx, postgres.ExtendLabTrafficCoverageParams{
		CoveredFrom: span.From, CoveredTo: span.To,
		EventID: eventID, TeamID: teamID, Surface: string(surface), Source: source, BootID: bootID, Partial: span.Partial,
		ToleranceSeconds: labTraffic.CoverageTolerance.Seconds(),
	})
	if err != nil || extended > 0 {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return r.q.CreateLabTrafficCoverage(ctx, postgres.CreateLabTrafficCoverageParams{
		ID: id, EventID: eventID, TeamID: teamID, Surface: string(surface), Source: source, BootID: bootID,
		CoveredFrom: span.From, CoveredTo: span.To, Partial: span.Partial,
	})
}

// Ask answers «did the user (nil = anybody in the team) touch the task before
// q.Before»: touched, untouched or unknown. Unknown means the collector did not
// watch the whole span, which is never read as «no touch». When q.Since is
// zero, the earliest deployment of the team's lab for the task is used; a task
// that was never deployed is unknown.
func (r *Repository) Ask(ctx context.Context, q labTraffic.Question) (labTraffic.Answer, error) {
	if len(q.Surfaces) == 0 {
		q.Surfaces = []labTraffic.Surface{labTraffic.SurfaceVPN}
	}
	if q.Since.IsZero() {
		deployed, err := r.q.GetLabDeployedSince(ctx, postgres.GetLabDeployedSinceParams{EventID: q.EventID, EventTeamID: q.TeamID, EventChallengeID: q.EventChallengeID})
		if err != nil {
			return labTraffic.Answer{}, err
		}
		if !deployed.Equal(zeroTime) {
			q.Since = deployed
		}
	}
	summary, err := r.q.SummarizeLabTouches(ctx, postgres.SummarizeLabTouchesParams{
		EventID: q.EventID, TeamID: q.TeamID, EventChallengeID: q.EventChallengeID, UserID: q.UserID, Before: q.Before,
	})
	if err != nil {
		return labTraffic.Answer{}, err
	}
	rows := make([]labTraffic.Aggregate, 0, len(summary))
	for _, row := range summary {
		aggregate := labTraffic.Aggregate{Surface: labTraffic.Surface(row.Surface), Attempts: row.Attempts, FirstSeenAt: row.FirstSeenAt, BytesIn: row.BytesIn}
		if !row.FirstRespondedAt.Equal(zeroTime) {
			responded := row.FirstRespondedAt
			aggregate.FirstRespondAt = &responded
		}
		rows = append(rows, aggregate)
	}
	coverage := map[labTraffic.Surface][]labTraffic.Coverage{}
	if !q.Since.IsZero() {
		spans, err := r.q.ListLabTrafficCoverage(ctx, postgres.ListLabTrafficCoverageParams{EventID: q.EventID, TeamID: q.TeamID, Since: q.Since, Until: q.Before})
		if err != nil {
			return labTraffic.Answer{}, err
		}
		for _, span := range spans {
			surface := labTraffic.Surface(span.Surface)
			coverage[surface] = append(coverage[surface], labTraffic.Coverage{From: span.CoveredFrom, To: span.CoveredTo, Partial: span.Partial})
		}
	}
	return labTraffic.Classify(q, rows, coverage), nil
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
