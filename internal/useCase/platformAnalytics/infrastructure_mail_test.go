package platformAnalytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

// infraMailStore serves the infrastructure and mail ports and records what
// it was asked.
type infraMailStore struct {
	Store

	calls      int
	bucket     string
	step       float64
	limit      int32
	filter     platformAnalyticsRepo.MailFilter
	options    bool
	optChannel string
	errCalls   int
	err        error

	counts   platformAnalyticsRepo.InfraStandCounts
	kinds    platformAnalyticsRepo.InfraKindCounts
	resEvent labMonitoringModel.Resources
	resMods  labMonitoringModel.Resources
	kindHrs  []platformAnalyticsRepo.InfraKindHours
	kindPeak map[[2]bool][]platformAnalyticsRepo.InfraPeak
	hours    platformAnalyticsRepo.InfraStandHoursReport
	peaks    []platformAnalyticsRepo.InfraPeak
	failures []platformAnalyticsRepo.InfraFailure
	capacity []platformAnalyticsRepo.InfraCapacity

	summary platformAnalyticsRepo.MailSummary
	errs    []platformAnalyticsRepo.MailError
	opts    platformAnalyticsRepo.MailOptions
	funnels dispatchModel.MailFunnels
}

func (s *infraMailStore) InfraStandCounts(context.Context) (platformAnalyticsRepo.InfraStandCounts, error) {
	s.calls++
	return s.counts, s.err
}

func (s *infraMailStore) InfraKindCounts(context.Context, time.Time) (platformAnalyticsRepo.InfraKindCounts, error) {
	return s.kinds, s.err
}

func (s *infraMailStore) InfraStandResources(context.Context, time.Time) (labMonitoringModel.Resources, labMonitoringModel.Resources, error) {
	return s.resEvent, s.resMods, s.err
}

func (s *infraMailStore) InfraHoursByKind(context.Context, time.Time, time.Time, time.Time) ([]platformAnalyticsRepo.InfraKindHours, error) {
	return s.kindHrs, s.err
}

func (s *infraMailStore) InfraPeaksByKind(_ context.Context, _, _, _ time.Time, _ string, withStands, withTests bool) ([]platformAnalyticsRepo.InfraPeak, error) {
	return s.kindPeak[[2]bool{withStands, withTests}], s.err
}

func (s *infraMailStore) InfraStandHours(_ context.Context, _, _, _ time.Time, limit int32) (platformAnalyticsRepo.InfraStandHoursReport, error) {
	s.limit = limit
	return s.hours, s.err
}

func (s *infraMailStore) InfraPeaks(_ context.Context, _, _, _ time.Time, bucket string) ([]platformAnalyticsRepo.InfraPeak, error) {
	s.bucket = bucket
	return s.peaks, s.err
}

func (s *infraMailStore) InfraFailures(context.Context, time.Time, time.Time) ([]platformAnalyticsRepo.InfraFailure, error) {
	return s.failures, s.err
}

func (s *infraMailStore) InfraCapacity(_ context.Context, _, _ time.Time, step float64) ([]platformAnalyticsRepo.InfraCapacity, error) {
	s.step = step
	return s.capacity, s.err
}

func (s *infraMailStore) MailSummary(_ context.Context, _, _ time.Time, f platformAnalyticsRepo.MailFilter) (platformAnalyticsRepo.MailSummary, error) {
	s.calls++
	s.filter = f
	return s.summary, s.err
}

func (s *infraMailStore) MailErrors(_ context.Context, _, _ time.Time, _ platformAnalyticsRepo.MailFilter, limit int32) ([]platformAnalyticsRepo.MailError, error) {
	s.errCalls++
	s.limit = limit
	return s.errs, s.err
}

func (s *infraMailStore) MailOptions(_ context.Context, _, _ time.Time, channel string, includeTests bool) (platformAnalyticsRepo.MailOptions, error) {
	s.optChannel = channel
	s.options = includeTests
	return s.opts, s.err
}

func (s *infraMailStore) MailFunnels(context.Context, time.Time, time.Time) (dispatchModel.MailFunnels, error) {
	return s.funnels, s.err
}

var imNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func imUseCase(store *infraMailStore) *PlatformAnalyticsUseCase {
	return New(Dependencies{Store: store, Now: func() time.Time { return imNow }})
}

func imDays(days int) (*time.Time, *time.Time) {
	from := imNow.AddDate(0, 0, -days)
	to := imNow
	return &from, &to
}

// The period is aligned outward to whole UTC days, so 13 days back from noon
// is 14 days (hourly) and 14 days back is 15 (daily).
func TestInfrastructureUsesHourlyBucketsUpToTwoWeeksThenDaily(t *testing.T) {
	for _, tc := range []struct {
		days   int
		bucket string
	}{{7, "hour"}, {13, "hour"}, {14, "day"}, {30, "day"}} {
		store := &infraMailStore{}
		from, to := imDays(tc.days)
		if _, err := imUseCase(store).GetInfrastructure(context.Background(), from, to); err != nil {
			t.Fatal(err)
		}
		if store.bucket != tc.bucket {
			t.Fatalf("%d days: bucket = %q, want %q", tc.days, store.bucket, tc.bucket)
		}
	}
}

func TestInfrastructureBuildsTheReport(t *testing.T) {
	store := &infraMailStore{
		counts: platformAnalyticsRepo.InfraStandCounts{Creating: 2, Ready: 5, Failed: 1, Removed: 9},
		hours:  platformAnalyticsRepo.InfraStandHoursReport{TotalHours: 12.5, TotalEvents: 3, Top: []platformAnalyticsRepo.InfraStandHours{{EventName: "A", Hours: 10, Stands: 4}}},
		peaks:  []platformAnalyticsRepo.InfraPeak{{At: imNow, Peak: 3}, {At: imNow.Add(time.Hour), Peak: 7}},
		failures: []platformAnalyticsRepo.InfraFailure{
			{Code: "image_pull", Labs: 3, Stands: 1, Events: 1},
			{Code: "deploy_timeout", Labs: 2, Stands: 2, Events: 2},
		},
		capacity: []platformAnalyticsRepo.InfraCapacity{{At: imNow, AllocatableCPUMillis: 8000, RequestedCPUMillis: 2000, Agents: 2}},
		resEvent: labMonitoringModel.Resources{Known: true, Available: true, CPUMillicores: 900, MemoryBytes: 4096},
		kinds:    platformAnalyticsRepo.InfraKindCounts{Moderators: platformAnalyticsRepo.InfraStandCounts{Creating: 1, Ready: 1}, TestActive: 3, TestExpired: 1},
		// Only the test kind has time; the order of the rows is fixed by the use case.
		kindHrs: []platformAnalyticsRepo.InfraKindHours{{Kind: "test", Hours: 2.5, Labs: 4}, {Kind: "event", Hours: 10, Labs: 4}},
		kindPeak: map[[2]bool][]platformAnalyticsRepo.InfraPeak{
			{false, true}: {{At: imNow, Peak: 2}},
			{true, true}:  {{At: imNow, Peak: 6}, {At: imNow.Add(time.Hour), Peak: 9}},
		},
	}
	from, to := imDays(7)
	got, err := imUseCase(store).GetInfrastructure(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stands.Active != 7 || got.Stands.Failed != 1 || got.Stands.Removed != 9 {
		t.Fatalf("stands = %+v", got.Stands)
	}
	if got.StandHours.TotalHours != 12.5 || len(got.StandHours.Events) != 1 || store.limit != standHoursTop {
		t.Fatalf("stand hours = %+v limit=%d", got.StandHours, store.limit)
	}
	if got.Resources.Event.CPUMillicores != 900 || got.Resources.Moderators.Known || got.Resources.Test.Known {
		t.Fatalf("resources = %+v", got.Resources)
	}
	if got.Moderators.Active != 2 || got.TestLabs.Active != 3 || got.TestLabs.Expired != 1 {
		t.Fatalf("moderators = %+v, test labs = %+v", got.Moderators, got.TestLabs)
	}
	// Team stand hours stay as they were; the test lab time is added on top.
	if got.StandHours.AllHours != 15 || len(got.StandHours.Kinds) != 3 ||
		got.StandHours.Kinds[0] != (StandHoursKindView{Kind: "event", Hours: 10, Labs: 4}) ||
		got.StandHours.Kinds[1] != (StandHoursKindView{Kind: "moderators"}) ||
		got.StandHours.Kinds[2] != (StandHoursKindView{Kind: "test", Hours: 2.5, Labs: 4}) {
		t.Fatalf("hours by kind = %+v all = %v", got.StandHours.Kinds, got.StandHours.AllHours)
	}
	if len(got.TestLabPeaks) != 1 || got.TestLabPeaks[0].Peak != 2 || len(got.AllPeaks) != 2 || got.AllPeakMax != 9 {
		t.Fatalf("test lab peaks = %+v, all peaks = %+v max=%d", got.TestLabPeaks, got.AllPeaks, got.AllPeakMax)
	}
	if got.PeakMax != 7 || len(got.Peaks) != 2 {
		t.Fatalf("peaks = %+v max=%d", got.Peaks, got.PeakMax)
	}
	if got.FailedLabs != 5 || got.FailedStands != 3 || len(got.Failures) != 2 {
		t.Fatalf("failures = %+v", got)
	}
	if len(got.Capacity) != 1 || got.Capacity[0].Agents != 2 {
		t.Fatalf("capacity = %+v", got.Capacity)
	}
}

func TestInfrastructureFailureIsWrappedAndNotCached(t *testing.T) {
	store := &infraMailStore{err: errors.New("db down")}
	uc := imUseCase(store)
	from, to := imDays(7)
	if _, err := uc.GetInfrastructure(context.Background(), from, to); err == nil {
		t.Fatal("expected an error")
	}
	store.err = nil
	if _, err := uc.GetInfrastructure(context.Background(), from, to); err != nil {
		t.Fatalf("a failed load was cached: %v", err)
	}
}

func TestInfrastructureReportIsCachedPerPeriod(t *testing.T) {
	store := &infraMailStore{}
	uc := imUseCase(store)
	from, to := imDays(7)
	for range 3 {
		if _, err := uc.GetInfrastructure(context.Background(), from, to); err != nil {
			t.Fatal(err)
		}
	}
	if store.calls != 1 {
		t.Fatalf("loads = %d, want 1", store.calls)
	}
	from30, to30 := imDays(30)
	if _, err := uc.GetInfrastructure(context.Background(), from30, to30); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 {
		t.Fatalf("a different period must load again, loads = %d", store.calls)
	}
}

func TestCapacityStepKeepsAtMostAboutThreeHundredPoints(t *testing.T) {
	day := platformAnalyticsModel.Day
	for _, tc := range []struct {
		span time.Duration
		want time.Duration
	}{
		{day, 5 * time.Minute},
		{7 * day, 34 * time.Minute},
		{30 * day, 144 * time.Minute},
		{365 * day, 1752 * time.Minute},
	} {
		period := platformAnalyticsModel.Period{From: imNow.Add(-tc.span), To: imNow}
		step := capacityStep(period, imNow.Add(time.Hour))
		if step != tc.want {
			t.Fatalf("span %v: step = %v, want %v", tc.span, step, tc.want)
		}
		if points := tc.span / step; points > capacityMaxPoints {
			t.Fatalf("span %v: %d points", tc.span, points)
		}
	}
	// A period reaching into the future is measured up to now.
	period := platformAnalyticsModel.Period{From: imNow.Add(-day), To: imNow.Add(30 * day)}
	if step := capacityStep(period, imNow); step != 5*time.Minute {
		t.Fatalf("future end: step = %v", step)
	}
}

func TestMailReportTotalsRatesAndDenseDays(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 9, n, 0, 0, 0, 0, time.UTC) }
	store := &infraMailStore{
		summary: platformAnalyticsRepo.MailSummary{
			Days: []platformAnalyticsRepo.MailDay{
				{Day: day(28), MailCount: platformAnalyticsRepo.MailCount{Sent: 8, Failed: 2}},
				{Day: day(30), MailCount: platformAnalyticsRepo.MailCount{Sent: 10, Fallbacks: 1}},
			},
			Transports: []platformAnalyticsRepo.MailKey{
				{Key: "event", MailCount: platformAnalyticsRepo.MailCount{Sent: 2}},
				{Key: "platform", MailCount: platformAnalyticsRepo.MailCount{Sent: 16, Failed: 2}},
			},
			Types: []platformAnalyticsRepo.MailKey{{Key: "user.welcome", MailCount: platformAnalyticsRepo.MailCount{Sent: 18, Failed: 2}}},
		},
		errs: []platformAnalyticsRepo.MailError{{Code: "550 5.1.1", Message: "rejected <address>", Total: 2}},
		opts: platformAnalyticsRepo.MailOptions{Transports: []string{"event", "platform"}, Types: []string{"user.welcome"}},
	}
	from := day(28)
	to := day(31)
	got, err := imUseCase(store).GetMail(context.Background(), &from, &to, "", " platform ", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Daily) != 3 || got.Daily[1].Sent != 0 || got.Daily[1].Failed != 0 || got.Daily[2].Fallbacks != 1 {
		t.Fatalf("daily = %+v", got.Daily)
	}
	if got.Sent != 18 || got.Failed != 2 || got.Total != 20 || got.FailureRate != 0.1 || got.Fallbacks != 1 {
		t.Fatalf("totals = %+v", got)
	}
	if got.ByTransport[0].Key != "platform" || got.ByTransport[0].Total != 18 || got.ByTransport[1].Key != "event" {
		t.Fatalf("transports must be ordered by volume: %+v", got.ByTransport)
	}
	if store.filter.Transport != "platform" || store.filter.IncludeTests || store.limit != mailErrorsTop {
		t.Fatalf("filter = %+v limit=%d", store.filter, store.limit)
	}
	if len(got.Errors) != 1 || got.Errors[0].Code != "550 5.1.1" {
		t.Fatalf("errors = %+v", got.Errors)
	}
	if got.Options.Transports[1] != "platform" || got.Transport != "platform" {
		t.Fatalf("options = %+v transport=%q", got.Options, got.Transport)
	}
}

func TestMailEmptyPeriodHasNoRateAndKeepsTheSelectedFilterOption(t *testing.T) {
	store := &infraMailStore{}
	from, to := imDays(7)
	got, err := imUseCase(store).GetMail(context.Background(), from, to, "", "env", "smtp_test", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 || got.FailureRate != 0 || len(got.Daily) == 0 {
		t.Fatalf("empty report = %+v", got)
	}
	if len(got.Options.Transports) != 1 || got.Options.Transports[0] != "env" || got.Options.Types[0] != "smtp_test" || !store.options {
		t.Fatalf("options = %+v includeTests=%v", got.Options, store.options)
	}
	if got.ByTransport == nil || got.ByType == nil || got.Errors == nil {
		t.Fatal("lists must be empty, not nil")
	}
}

func TestMailFiltersAreCachedSeparately(t *testing.T) {
	store := &infraMailStore{}
	uc := imUseCase(store)
	from, to := imDays(7)
	for _, tc := range []struct {
		channel, transport, kind string
		tests                    bool
	}{{"", "", "", false}, {"", "", "", false}, {"", "event", "", false}, {"", "", "x", false}, {"", "", "", true}, {"in_app", "", "", false}, {"email", "", "", false}} {
		if _, err := uc.GetMail(context.Background(), from, to, tc.channel, tc.transport, tc.kind, tc.tests); err != nil {
			t.Fatal(err)
		}
	}
	if store.calls != 6 {
		t.Fatalf("loads = %d, want 6 (one repeat served from the cache)", store.calls)
	}
}

func TestMailChannelFilterReachesTheStoreAndSkipsSMTPErrorsForInApp(t *testing.T) {
	store := &infraMailStore{summary: platformAnalyticsRepo.MailSummary{
		Channels: []platformAnalyticsRepo.MailKey{
			{Key: "in_app", MailCount: platformAnalyticsRepo.MailCount{Sent: 5}},
			{Key: "email", MailCount: platformAnalyticsRepo.MailCount{Sent: 8, Failed: 2, Deferred: 3}},
		},
		Days: []platformAnalyticsRepo.MailDay{{Day: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), MailCount: platformAnalyticsRepo.MailCount{Deferred: 3}}},
	}}
	from, to := imDays(7)
	view, err := imUseCase(store).GetMail(context.Background(), from, to, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if store.filter.Channel != "" || store.errCalls != 1 || store.optChannel != "" {
		t.Fatalf("all: filter = %+v errCalls = %d", store.filter, store.errCalls)
	}
	if len(view.ByChannel) != 2 || view.ByChannel[0].Key != "email" || view.ByChannel[0].Total != 10 || view.ByChannel[0].Deferred != 3 || view.ByChannel[1].Key != "in_app" {
		t.Fatalf("channels must be ordered by volume: %+v", view.ByChannel)
	}
	if view.Deferred != 3 || view.Total != 0 {
		t.Fatalf("deferred is counted apart from the total: %+v", view)
	}

	store.errCalls = 0
	view, err = imUseCase(store).GetMail(context.Background(), from, to, "in_app", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if store.filter.Channel != "in_app" || store.optChannel != "in_app" || store.errCalls != 0 || view.Channel != "in_app" || view.Errors == nil || len(view.Errors) != 0 {
		t.Fatalf("in_app: filter = %+v optChannel = %q errCalls = %d errors = %#v", store.filter, store.optChannel, store.errCalls, view.Errors)
	}

	// An unknown channel reads as all.
	if _, err = imUseCase(store).GetMail(context.Background(), from, to, "sms", "", "", false); err != nil || store.filter.Channel != "" {
		t.Fatalf("unknown channel: filter = %+v err = %v", store.filter, err)
	}
}

func TestMailRejectsAnInvalidPeriod(t *testing.T) {
	from := imNow
	if _, err := imUseCase(&infraMailStore{}).GetMail(context.Background(), &from, &from, "", "", "", false); err == nil {
		t.Fatal("expected an invalid period error")
	}
}

func TestGetMailIncludesTheOutcomeFunnels(t *testing.T) {
	store := &infraMailStore{funnels: dispatchModel.MailFunnels{
		InvitationsSent: 5, InvitationsAccepted: 2, InvitationAcceptSamples: 2, InvitationAcceptMedianSeconds: 3600,
		RegistrationsStarted: 10, RegistrationsCompleted: 7,
		ApplicationsSubmitted: 4, ApplicationsApproved: 1, ApplicationsRejected: 1, ApplicationDecisionSamples: 2, ApplicationDecisionMedianSeconds: 120,
	}}
	from, to := imDays(7)
	view, err := imUseCase(store).GetMail(context.Background(), from, to, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	f := view.Funnels
	if *f.Invitations.AcceptRate != 0.4 || *f.Invitations.MedianAcceptSeconds != 3600 || *f.Registration.CompletionRate != 0.7 ||
		f.Applications.Decided != 2 || *f.Applications.DecidedRate != 0.5 || *f.Applications.MedianDecisionSeconds != 120 {
		t.Fatalf("funnels: %+v", f)
	}
	store.err = errors.New("boom")
	if _, err = imUseCase(&infraMailStore{err: store.err}).GetMail(context.Background(), from, to, "", "", "", false); err == nil {
		t.Fatal("a failing store fails the report")
	}
}
