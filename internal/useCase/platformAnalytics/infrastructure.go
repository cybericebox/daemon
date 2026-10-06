package platformAnalytics

import (
	"context"
	"math"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

const (
	// hourlyMaxDays is the longest period shown per hour; longer ones per day.
	hourlyMaxDays = 14
	// standHoursTop is how many events the stand-hours ranking holds.
	standHoursTop = 20
	// capacityMaxPoints bounds the capacity series of one period.
	capacityMaxPoints = 300
	// capacityMinStep is the finest capacity bucket (the agents report about
	// every 30 s, so a minute is already a fair sample).
	capacityMinStep = time.Minute
)

// InfrastructureStore is the read port of the infrastructure section.
type InfrastructureStore interface {
	InfraStandHours(ctx context.Context, from, to, now time.Time, limit int32) (platformAnalyticsRepo.InfraStandHoursReport, error)
	InfraPeaks(ctx context.Context, from, to, now time.Time, bucket string) ([]platformAnalyticsRepo.InfraPeak, error)
	InfraFailures(ctx context.Context, from, to time.Time) ([]platformAnalyticsRepo.InfraFailure, error)
	InfraCapacity(ctx context.Context, from, to time.Time, stepSeconds float64) ([]platformAnalyticsRepo.InfraCapacity, error)
	InfraStandCounts(ctx context.Context) (platformAnalyticsRepo.InfraStandCounts, error)
}

// InfrastructureKindStore is the read port of the lab-kind split: event team
// stands, the moderators team stand and the catalog test labs.
type InfrastructureKindStore interface {
	InfraHoursByKind(ctx context.Context, from, to, now time.Time) ([]platformAnalyticsRepo.InfraKindHours, error)
	InfraPeaksByKind(ctx context.Context, from, to, now time.Time, bucket string, withStands, withTests bool) ([]platformAnalyticsRepo.InfraPeak, error)
	InfraKindCounts(ctx context.Context, now time.Time) (platformAnalyticsRepo.InfraKindCounts, error)
	InfraStandResources(ctx context.Context, now time.Time) (event, moderators labMonitoringModel.Resources, err error)
}

// GetInfrastructure builds the infrastructure report. A stand is active while
// it is creating or ready (the admin infrastructure summary's definition);
// stand-hours are the summed active time, peaks the most concurrently active
// stands per hour (periods up to 14 days) or per day. Stands, StandHours.TotalHours
// and Peaks keep counting the team stands only (the moderators team included),
// so they stay comparable; the catalog test labs and the moderators team are
// reported next to them by kind.
func (u *PlatformAnalyticsUseCase) GetInfrastructure(ctx context.Context, from, to *time.Time) (InfrastructureView, error) {
	period, err := u.period(from, to)
	if err != nil {
		return InfrastructureView{}, err
	}
	return cached(ctx, u, "infrastructure|"+period.Key(), func(ctx context.Context) (InfrastructureView, error) {
		now := u.now().UTC()
		bucket := "day"
		if period.Days() <= hourlyMaxDays {
			bucket = "hour"
		}
		view := InfrastructureView{Period: period, Bucket: bucket}

		counts, err := u.store.InfraStandCounts(ctx)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to count stands")
		}
		view.Stands = StandCountsView{Active: counts.Creating + counts.Ready, Creating: counts.Creating, Ready: counts.Ready, Failed: counts.Failed, Removed: counts.Removed}

		kinds, err := u.store.InfraKindCounts(ctx, now)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to count test laboratories")
		}
		view.Moderators = StandCountsView{Active: kinds.Moderators.Creating + kinds.Moderators.Ready, Creating: kinds.Moderators.Creating, Ready: kinds.Moderators.Ready, Failed: kinds.Moderators.Failed, Removed: kinds.Moderators.Removed}
		view.TestLabs = TestLabCountsView{Active: kinds.TestActive, Expired: kinds.TestExpired}

		hours, err := u.store.InfraStandHours(ctx, period.From, period.To, now, standHoursTop)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read stand hours")
		}
		view.StandHours = StandHoursView{TotalHours: hours.TotalHours, TotalEvents: hours.TotalEvents, Events: make([]StandHoursEventView, 0, len(hours.Top))}
		for _, h := range hours.Top {
			view.StandHours.Events = append(view.StandHours.Events, StandHoursEventView{EventID: h.EventID, EventName: h.EventName, Hours: h.Hours, Stands: h.Stands})
		}

		resources, err := u.resourcesNow(ctx, now)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read lab resources")
		}
		view.Resources = resources

		kindHours, err := u.store.InfraHoursByKind(ctx, period.From, period.To, now)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read lab hours by kind")
		}
		view.StandHours.Kinds = kindHoursViews(kindHours)
		view.StandHours.AllHours = hours.TotalHours
		for _, k := range view.StandHours.Kinds {
			if k.Kind == platformAnalyticsRepo.InfraKindTest {
				view.StandHours.AllHours += k.Hours
			}
		}

		peaks, err := u.store.InfraPeaks(ctx, period.From, period.To, now, bucket)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read stand peaks")
		}
		view.Peaks = make([]PeakPointView, 0, len(peaks))
		for _, p := range peaks {
			view.Peaks = append(view.Peaks, PeakPointView{At: p.At, Peak: p.Peak})
			view.PeakMax = max(view.PeakMax, p.Peak)
		}

		testPeaks, err := u.store.InfraPeaksByKind(ctx, period.From, period.To, now, bucket, false, true)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read test lab peaks")
		}
		view.TestLabPeaks = make([]PeakPointView, 0, len(testPeaks))
		for _, p := range testPeaks {
			view.TestLabPeaks = append(view.TestLabPeaks, PeakPointView{At: p.At, Peak: p.Peak})
		}
		allPeaks, err := u.store.InfraPeaksByKind(ctx, period.From, period.To, now, bucket, true, true)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read lab peaks")
		}
		view.AllPeaks = make([]PeakPointView, 0, len(allPeaks))
		for _, p := range allPeaks {
			view.AllPeaks = append(view.AllPeaks, PeakPointView{At: p.At, Peak: p.Peak})
			view.AllPeakMax = max(view.AllPeakMax, p.Peak)
		}

		failures, err := u.store.InfraFailures(ctx, period.From, period.To)
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read stand failures")
		}
		view.Failures = make([]FailureReasonView, 0, len(failures))
		for _, f := range failures {
			view.Failures = append(view.Failures, FailureReasonView{Code: f.Code, Labs: f.Labs, Stands: f.Stands, Events: f.Events, LastAt: f.LastAt})
			view.FailedLabs += f.Labs
			view.FailedStands += f.Stands
		}

		step := capacityStep(period, now)
		capacity, err := u.store.InfraCapacity(ctx, period.From, period.To, step.Seconds())
		if err != nil {
			return InfrastructureView{}, fail(err, "Failed to read cluster capacity")
		}
		view.CapacityStepSeconds = int64(step.Seconds())
		view.Capacity = make([]CapacityPointView, 0, len(capacity))
		for _, c := range capacity {
			view.Capacity = append(view.Capacity, CapacityPointView{
				At: c.At, AllocatableCPUMillicores: c.AllocatableCPUMillis, RequestedCPUMillicores: c.RequestedCPUMillis,
				AllocatableMemoryBytes: c.AllocatableMemory, RequestedMemoryBytes: c.RequestedMemory, Agents: c.Agents,
			})
		}
		return view, nil
	})
}

// resourcesNow sums what the labs use right now per kind. The test labs come from
// the optional provider (the infrastructure agent); without it they stay unknown.
func (u *PlatformAnalyticsUseCase) resourcesNow(ctx context.Context, now time.Time) (ResourcesByKindView, error) {
	event, moderators, err := u.store.InfraStandResources(ctx, now)
	if err != nil {
		return ResourcesByKindView{}, err
	}
	out := ResourcesByKindView{Event: event, Moderators: moderators}
	if u.testLabResources != nil {
		// Live usage of test labs is best effort: a failed read leaves them unknown.
		if test, testErr := u.testLabResources(ctx); testErr == nil {
			out.Test = test
		}
	}
	return out, nil
}

// kindHoursViews lists the three kinds in a fixed order, a kind without rows as zero.
func kindHoursViews(rows []platformAnalyticsRepo.InfraKindHours) []StandHoursKindView {
	byKind := make(map[string]platformAnalyticsRepo.InfraKindHours, len(rows))
	for _, row := range rows {
		byKind[row.Kind] = row
	}
	out := make([]StandHoursKindView, 0, 3)
	for _, kind := range []string{platformAnalyticsRepo.InfraKindEvent, platformAnalyticsRepo.InfraKindModerators, platformAnalyticsRepo.InfraKindTest} {
		out = append(out, StandHoursKindView{Kind: kind, Hours: byKind[kind].Hours, Labs: byKind[kind].Labs})
	}
	return out
}

// capacityStep picks the capacity bucket so the period has at most
// capacityMaxPoints points, in whole minutes.
func capacityStep(period platformAnalyticsModel.Period, now time.Time) time.Duration {
	end := period.To
	if now.Before(end) {
		end = now
	}
	span := end.Sub(period.From)
	step := time.Duration(math.Ceil(span.Seconds()/capacityMaxPoints/60)) * time.Minute
	return max(step, capacityMinStep)
}
