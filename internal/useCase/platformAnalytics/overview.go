package platformAnalytics

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

// OverviewStore is the read port of the overview section.
type OverviewStore interface {
	OverviewUsers(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewUsers, error)
	ActiveUsers(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewActiveUsers, error)
	OverviewEvents(ctx context.Context, w platformAnalyticsRepo.OverviewWindow, now time.Time) (platformAnalyticsRepo.OverviewEvents, error)
	OverviewParticipants(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewParticipants, error)
	OverviewActivity(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewActivity, error)
	OverviewMail(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewMail, error)
	OverviewStands(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewStands, error)
	OverviewUsersDays(ctx context.Context, from, to time.Time) ([]platformAnalyticsRepo.OverviewUsersDay, error)
	OverviewActivityDays(ctx context.Context, from, to time.Time) ([]platformAnalyticsRepo.OverviewActivityDay, error)
	OverviewMailDays(ctx context.Context, from, to time.Time) ([]platformAnalyticsRepo.OverviewMailDay, error)
}

// GetOverview is the «Огляд» report: headline numbers for the period with the
// change against the previous period of the same length (none for all time),
// and three daily series. Nil bounds: a missing `to` is now, a missing `from`
// is all time.
func (u *PlatformAnalyticsUseCase) GetOverview(ctx context.Context, from, to *time.Time) (OverviewView, error) {
	period, err := u.period(from, to)
	if err != nil {
		return OverviewView{}, err
	}
	return cached(ctx, u, "overview:"+period.Key(), func(ctx context.Context) (OverviewView, error) {
		return u.loadOverview(ctx, period)
	})
}

// overviewPeriodWindow is the repository window of a period; without a previous
// period the previous window is empty.
func overviewPeriodWindow(p platformAnalyticsModel.Period) platformAnalyticsRepo.OverviewWindow {
	w := platformAnalyticsRepo.OverviewWindow{From: p.From, To: p.To, PrevFrom: p.To, PrevTo: p.To}
	if prev, ok := p.Previous(); ok {
		w.PrevFrom, w.PrevTo = prev.From, prev.To
	}
	return w
}

// overviewMetricOf pairs a value with the previous period's one (nil for all time).
func overviewMetricOf(p platformAnalyticsModel.Period, value, previous int64) OverviewMetricView {
	m := OverviewMetricView{Value: value}
	if !p.All {
		m.Previous = &previous
	}
	return m
}

// overviewTrimLeading drops the leading empty days of an all-time series (the
// platform's clamped start has no data), keeping at least the last day.
func overviewTrimLeading[T any](p platformAnalyticsModel.Period, days []T, empty func(T) bool) []T {
	if !p.All {
		return days
	}
	i := 0
	for i < len(days)-1 && empty(days[i]) {
		i++
	}
	return days[i:]
}

func (u *PlatformAnalyticsUseCase) loadOverview(ctx context.Context, p platformAnalyticsModel.Period) (OverviewView, error) {
	w := overviewPeriodWindow(p)
	now := u.now()
	var (
		users        platformAnalyticsRepo.OverviewUsers
		active       platformAnalyticsRepo.OverviewActiveUsers
		events       platformAnalyticsRepo.OverviewEvents
		participants platformAnalyticsRepo.OverviewParticipants
		activity     platformAnalyticsRepo.OverviewActivity
		mail         platformAnalyticsRepo.OverviewMail
		stands       platformAnalyticsRepo.OverviewStands
		usersDays    []platformAnalyticsRepo.OverviewUsersDay
		activityDays []platformAnalyticsRepo.OverviewActivityDay
		mailDays     []platformAnalyticsRepo.OverviewMailDay
	)
	g, gctx := errgroup.WithContext(ctx)
	run := func(message string, load func(context.Context) error) {
		g.Go(func() error {
			if err := load(gctx); err != nil {
				return fail(err, message)
			}
			return nil
		})
	}
	run("Failed to count users", func(c context.Context) (err error) { users, err = u.store.OverviewUsers(c, w); return })
	run("Failed to count active users", func(c context.Context) (err error) { active, err = u.store.ActiveUsers(c, w); return })
	run("Failed to count events", func(c context.Context) (err error) { events, err = u.store.OverviewEvents(c, w, now); return })
	run("Failed to count participants", func(c context.Context) (err error) {
		participants, err = u.store.OverviewParticipants(c, w)
		return
	})
	run("Failed to count attempts", func(c context.Context) (err error) { activity, err = u.store.OverviewActivity(c, w); return })
	run("Failed to count emails", func(c context.Context) (err error) { mail, err = u.store.OverviewMail(c, w); return })
	run("Failed to count stands", func(c context.Context) (err error) { stands, err = u.store.OverviewStands(c, w); return })
	run("Failed to read new users series", func(c context.Context) (err error) {
		usersDays, err = u.store.OverviewUsersDays(c, p.From, p.To)
		return
	})
	run("Failed to read attempts series", func(c context.Context) (err error) {
		activityDays, err = u.store.OverviewActivityDays(c, p.From, p.To)
		return
	})
	run("Failed to read emails series", func(c context.Context) (err error) {
		mailDays, err = u.store.OverviewMailDays(c, p.From, p.To)
		return
	})
	if err := g.Wait(); err != nil {
		return OverviewView{}, err
	}

	view := OverviewView{
		Period: overviewPeriodViewOf(p),
		Users:  OverviewUsersView{Total: users.Total, New: overviewMetricOf(p, users.New, users.NewPrev), Active: overviewMetricOf(p, active.Active, active.ActivePrev)},
		Events: OverviewEventsView{
			Draft: events.Draft, Published: events.Published, Running: events.Running, Finished: events.Finished, Archived: events.Archived,
			Total: events.Total, New: overviewMetricOf(p, events.New, events.NewPrev),
		},
		Participants: OverviewParticipantsView{
			Registered: overviewMetricOf(p, participants.Registered, participants.RegisteredPrev),
			Approved:   overviewMetricOf(p, participants.Approved, participants.ApprovedPrev),
		},
		Activity: OverviewActivityView{Attempts: overviewMetricOf(p, activity.Attempts, activity.AttemptsPrev), Solves: overviewMetricOf(p, activity.Solves, activity.SolvesPrev)},
		Mail:     OverviewMailView{Sent: overviewMetricOf(p, mail.Sent, mail.SentPrev), Failed: overviewMetricOf(p, mail.Failed, mail.FailedPrev)},
		Stands:   OverviewStandsView{Ready: stands.Ready, Creating: stands.Creating, Failed: stands.FailedNow, Failures: overviewMetricOf(p, stands.Failures, stands.FailuresPrev)},
		Series: OverviewSeriesView{
			NewUsers: make([]OverviewDayNewView, 0, len(usersDays)),
			Activity: make([]OverviewDayActivityView, 0, len(activityDays)),
			Mail:     make([]OverviewDayMailView, 0, len(mailDays)),
		},
	}
	for _, d := range overviewTrimLeading(p, usersDays, func(d platformAnalyticsRepo.OverviewUsersDay) bool { return d.New == 0 }) {
		view.Series.NewUsers = append(view.Series.NewUsers, OverviewDayNewView{Day: d.Day, New: d.New})
	}
	for _, d := range overviewTrimLeading(p, activityDays, func(d platformAnalyticsRepo.OverviewActivityDay) bool { return d.Attempts == 0 && d.Solves == 0 }) {
		view.Series.Activity = append(view.Series.Activity, OverviewDayActivityView{Day: d.Day, Attempts: d.Attempts, Solves: d.Solves})
	}
	for _, d := range overviewTrimLeading(p, mailDays, func(d platformAnalyticsRepo.OverviewMailDay) bool { return d.Sent == 0 && d.Failed == 0 }) {
		view.Series.Mail = append(view.Series.Mail, OverviewDayMailView{Day: d.Day, Sent: d.Sent, Failed: d.Failed})
	}
	return view, nil
}
