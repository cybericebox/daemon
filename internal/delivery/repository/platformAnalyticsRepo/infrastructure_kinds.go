package platformAnalyticsRepo

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

// InfrastructureKindQueries are the statements that split the infrastructure
// section by lab kind: event team stands, the moderators team stand and the
// catalog test labs.
type InfrastructureKindQueries interface {
	ListPlatformInfraHoursByKind(context.Context, postgres.ListPlatformInfraHoursByKindParams) ([]postgres.ListPlatformInfraHoursByKindRow, error)
	ListPlatformInfraPeaksByKind(context.Context, postgres.ListPlatformInfraPeaksByKindParams) ([]postgres.ListPlatformInfraPeaksByKindRow, error)
	CountPlatformStandsByKindStatus(context.Context) ([]postgres.CountPlatformStandsByKindStatusRow, error)
	CountPlatformTestLabs(context.Context, time.Time) (postgres.CountPlatformTestLabsRow, error)
	ListPlatformLabMonitoringCurrent(context.Context, postgres.ListPlatformLabMonitoringCurrentParams) ([]postgres.ListPlatformLabMonitoringCurrentRow, error)
}

// Lab kinds of the infrastructure analytics.
const (
	InfraKindEvent      = "event"
	InfraKindModerators = "moderators"
	InfraKindTest       = "test"
)

type (
	// InfraKindHours is the active time of one lab kind in the period.
	InfraKindHours struct {
		Kind string
		// Hours is the summed active time.
		Hours float64
		// Labs is the number of distinct stands or test labs that were active.
		Labs int64
	}

	// InfraKindCounts is the live picture: the moderators-team stands and the
	// catalog test labs (the event team stands are InfraStandCounts minus
	// the moderators part).
	InfraKindCounts struct {
		Moderators  InfraStandCounts
		TestActive  int64
		TestExpired int64
	}
)

// InfraHoursByKind reads the active time per lab kind; every kind has a row.
func (r *Repository) InfraHoursByKind(ctx context.Context, from, to, now time.Time) ([]InfraKindHours, error) {
	rows, err := r.q.ListPlatformInfraHoursByKind(ctx, postgres.ListPlatformInfraHoursByKindParams{FromAt: from, ToAt: to, NowAt: now})
	if err != nil {
		return nil, err
	}
	out := make([]InfraKindHours, 0, len(rows))
	for _, row := range rows {
		out = append(out, InfraKindHours{Kind: row.Kind, Hours: row.Hours, Labs: row.Labs})
	}
	return out, nil
}

// InfraPeaksByKind reads the concurrent active labs peak per bucket over team
// stands (withStands) and / or catalog test labs (withTests).
func (r *Repository) InfraPeaksByKind(ctx context.Context, from, to, now time.Time, bucket string, withStands, withTests bool) ([]InfraPeak, error) {
	rows, err := r.q.ListPlatformInfraPeaksByKind(ctx, postgres.ListPlatformInfraPeaksByKindParams{
		Bucket: bucket, FromAt: from, ToAt: to, NowAt: now, WithStands: withStands, WithTests: withTests,
	})
	if err != nil {
		return nil, err
	}
	out := make([]InfraPeak, 0, len(rows))
	for _, row := range rows {
		out = append(out, InfraPeak{At: row.BucketAt, Peak: row.Peak})
	}
	return out, nil
}

// InfraKindCounts reads the moderators-team stand counts and the catalog test
// lab counts (expired ones wait for the cleanup pass).
func (r *Repository) InfraKindCounts(ctx context.Context, now time.Time) (InfraKindCounts, error) {
	rows, err := r.q.CountPlatformStandsByKindStatus(ctx)
	if err != nil {
		return InfraKindCounts{}, err
	}
	var out InfraKindCounts
	for _, row := range rows {
		if !row.Moderators {
			continue
		}
		switch row.Status {
		case standCreating:
			out.Moderators.Creating += row.Total
		case standReady:
			out.Moderators.Ready += row.Total
		case standFailed:
			out.Moderators.Failed += row.Total
		case standRemoved:
			out.Moderators.Removed += row.Total
		}
	}
	tests, err := r.q.CountPlatformTestLabs(ctx, now)
	if err != nil {
		return InfraKindCounts{}, err
	}
	out.TestActive, out.TestExpired = tests.Active, tests.Expired
	return out, nil
}

// InfraStandResources sums what the labs of active events use right now: the
// event team stands and the moderators team stand apart.
func (r *Repository) InfraStandResources(ctx context.Context, now time.Time) (event, moderators labMonitoringModel.Resources, err error) {
	rows, err := r.q.ListPlatformLabMonitoringCurrent(ctx, postgres.ListPlatformLabMonitoringCurrentParams{Now: now})
	if err != nil {
		return event, moderators, err
	}
	for _, row := range rows {
		resources := labMonitoringModel.PayloadResources(row.Payload)
		if row.Moderators {
			moderators.Add(resources)
		} else {
			event.Add(resources)
		}
	}
	return event, moderators, nil
}
