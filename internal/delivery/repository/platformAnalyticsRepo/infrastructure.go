package platformAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// InfrastructureQueries are the sqlc statements of the infrastructure section.
type InfrastructureQueries interface {
	ListPlatformInfraStandHours(context.Context, postgres.ListPlatformInfraStandHoursParams) ([]postgres.ListPlatformInfraStandHoursRow, error)
	ListPlatformInfraPeaks(context.Context, postgres.ListPlatformInfraPeaksParams) ([]postgres.ListPlatformInfraPeaksRow, error)
	ListPlatformInfraFailures(context.Context, postgres.ListPlatformInfraFailuresParams) ([]postgres.ListPlatformInfraFailuresRow, error)
	ListPlatformInfraCapacity(context.Context, postgres.ListPlatformInfraCapacityParams) ([]postgres.ListPlatformInfraCapacityRow, error)
	CountPlatformStandsByStatus(context.Context) ([]postgres.CountPlatformStandsByStatusRow, error)
}

type (
	// InfraStandHours is the active stand time of one event in the period.
	InfraStandHours struct {
		EventID   uuid.UUID
		EventName string
		Hours     float64
		Stands    int64
	}

	// InfraStandHoursReport is the top events plus the totals over all events.
	InfraStandHoursReport struct {
		Top         []InfraStandHours
		TotalHours  float64
		TotalEvents int64
	}

	// InfraPeak is the peak of concurrently active stands in one bucket.
	InfraPeak struct {
		At   time.Time
		Peak int64
	}

	// InfraFailure is one failure reason code with its counts.
	InfraFailure struct {
		Code   string
		Labs   int64
		Stands int64
		Events int64
		LastAt time.Time
	}

	// InfraCapacity is the cluster capacity (all agents summed) at one bucket.
	InfraCapacity struct {
		At                   time.Time
		AllocatableCPUMillis int64
		RequestedCPUMillis   int64
		AllocatableMemory    int64
		RequestedMemory      int64
		Agents               int64
	}

	// InfraStandCounts is the current number of stands per status.
	InfraStandCounts struct {
		Creating, Ready, Failed, Removed int64
	}
)

// Stand statuses as stored (eventStand.Status).
const (
	standCreating int16 = 1
	standReady    int16 = 2
	standFailed   int16 = 3
	standRemoved  int16 = 4
)

// InfraStandHours reads the top events by stand-hours in [from, to), the open
// intervals ending at min(to, now).
func (r *Repository) InfraStandHours(ctx context.Context, from, to, now time.Time, limit int32) (InfraStandHoursReport, error) {
	rows, err := r.q.ListPlatformInfraStandHours(ctx, postgres.ListPlatformInfraStandHoursParams{FromAt: from, ToAt: to, NowAt: now, LimitVal: limit})
	if err != nil {
		return InfraStandHoursReport{}, err
	}
	out := InfraStandHoursReport{Top: make([]InfraStandHours, 0, len(rows))}
	for _, row := range rows {
		out.TotalHours, out.TotalEvents = row.TotalHours, row.TotalEvents
		out.Top = append(out.Top, InfraStandHours{EventID: row.EventID, EventName: row.EventName, Hours: row.Hours, Stands: row.Stands})
	}
	return out, nil
}

// InfraPeaks reads the concurrent active stands peak per bucket ("hour" or "day").
func (r *Repository) InfraPeaks(ctx context.Context, from, to, now time.Time, bucket string) ([]InfraPeak, error) {
	rows, err := r.q.ListPlatformInfraPeaks(ctx, postgres.ListPlatformInfraPeaksParams{Bucket: bucket, FromAt: from, ToAt: to, NowAt: now})
	if err != nil {
		return nil, err
	}
	out := make([]InfraPeak, 0, len(rows))
	for _, row := range rows {
		out = append(out, InfraPeak{At: row.BucketAt, Peak: row.Peak})
	}
	return out, nil
}

// InfraFailures reads the failed labs and stands per reason code.
func (r *Repository) InfraFailures(ctx context.Context, from, to time.Time) ([]InfraFailure, error) {
	rows, err := r.q.ListPlatformInfraFailures(ctx, postgres.ListPlatformInfraFailuresParams{FromAt: from, ToAt: to})
	if err != nil {
		return nil, err
	}
	out := make([]InfraFailure, 0, len(rows))
	for _, row := range rows {
		out = append(out, InfraFailure{Code: row.Code, Labs: row.Labs, Stands: row.Stands, Events: row.Events, LastAt: row.LastAt})
	}
	return out, nil
}

// InfraCapacity reads the cluster capacity downsampled to stepSeconds buckets.
func (r *Repository) InfraCapacity(ctx context.Context, from, to time.Time, stepSeconds float64) ([]InfraCapacity, error) {
	rows, err := r.q.ListPlatformInfraCapacity(ctx, postgres.ListPlatformInfraCapacityParams{StepSeconds: stepSeconds, FromAt: from, ToAt: to})
	if err != nil {
		return nil, err
	}
	out := make([]InfraCapacity, 0, len(rows))
	for _, row := range rows {
		out = append(out, InfraCapacity{
			At: row.BucketAt, AllocatableCPUMillis: row.AllocatableCpuMillicores, RequestedCPUMillis: row.RequestedCpuMillicores,
			AllocatableMemory: row.AllocatableMemoryBytes, RequestedMemory: row.RequestedMemoryBytes, Agents: row.Agents,
		})
	}
	return out, nil
}

// InfraStandCounts reads the current stand count per status (the same
// statement the admin infrastructure summary uses).
func (r *Repository) InfraStandCounts(ctx context.Context) (InfraStandCounts, error) {
	rows, err := r.q.CountPlatformStandsByStatus(ctx)
	if err != nil {
		return InfraStandCounts{}, err
	}
	var out InfraStandCounts
	for _, row := range rows {
		switch row.Status {
		case standCreating:
			out.Creating = row.Total
		case standReady:
			out.Ready = row.Total
		case standFailed:
			out.Failed = row.Total
		case standRemoved:
			out.Removed = row.Total
		}
	}
	return out, nil
}
