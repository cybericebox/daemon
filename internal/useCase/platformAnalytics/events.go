package platformAnalytics

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

const (
	// eventsTableLimit bounds the per-event table: the newest events of the
	// period; EventsView.EventsTotal tells how many there are.
	eventsTableLimit = 200
	// upcomingLimit is how many scheduled events the upcoming list shows.
	upcomingLimit = 10
)

// EventsStore is the read port of the events section.
type EventsStore interface {
	EventSeries(ctx context.Context, from, to time.Time) ([]platformAnalyticsRepo.EventDay, error)
	EventStatuses(ctx context.Context, from, to, asOf time.Time) ([]platformAnalyticsRepo.EventStatusCount, error)
	Events(ctx context.Context, from, to, asOf time.Time, limit int32) ([]platformAnalyticsRepo.EventRow, int64, error)
	UpcomingEvents(ctx context.Context, asOf time.Time, limit int32) ([]platformAnalyticsRepo.UpcomingEvent, error)
}

// eventStatusOrder lists the lifecycle statuses in the order the report shows
// them; every one is always present, with a zero count when empty.
var eventStatusOrder = []eventModel.LifecycleStatus{
	eventModel.LifecycleNotPublished, eventModel.LifecyclePublished, eventModel.LifecycleStarted,
	eventModel.LifecycleFinished, eventModel.LifecycleWithdrawn,
}

// EventStatusName is the wire name of a lifecycle status.
func EventStatusName(status int16) string {
	switch eventModel.LifecycleStatus(status) {
	case eventModel.LifecyclePublished:
		return "published"
	case eventModel.LifecycleStarted:
		return "started"
	case eventModel.LifecycleFinished:
		return "finished"
	case eventModel.LifecycleWithdrawn:
		return "withdrawn"
	default:
		return "not_published"
	}
}

// GetEvents is the «Заходи» report: events and registrations over the period,
// events by lifecycle status, the newest events with their aggregates, and the
// next scheduled events. Aggregates only.
func (u *PlatformAnalyticsUseCase) GetEvents(ctx context.Context, from, to *time.Time) (EventsView, error) {
	period, err := u.period(from, to)
	if err != nil {
		return EventsView{}, err
	}
	return cached(ctx, u, "events:"+period.Key(), func(ctx context.Context) (EventsView, error) {
		return u.loadEvents(ctx, period)
	})
}

func (u *PlatformAnalyticsUseCase) loadEvents(ctx context.Context, period platformAnalyticsModel.Period) (EventsView, error) {
	asOf := u.now()
	series, err := u.store.EventSeries(ctx, period.From, period.To)
	if err != nil {
		return EventsView{}, fail(err, "Failed to read the events series")
	}
	statuses, err := u.store.EventStatuses(ctx, period.From, period.To, asOf)
	if err != nil {
		return EventsView{}, fail(err, "Failed to read the events by status")
	}
	rows, total, err := u.store.Events(ctx, period.From, period.To, asOf, eventsTableLimit)
	if err != nil {
		return EventsView{}, fail(err, "Failed to read the events")
	}
	upcoming, err := u.store.UpcomingEvents(ctx, asOf, upcomingLimit)
	if err != nil {
		return EventsView{}, fail(err, "Failed to read the upcoming events")
	}
	return buildEventsView(period, series, statuses, rows, total, upcoming), nil
}

func buildEventsView(period platformAnalyticsModel.Period, series []platformAnalyticsRepo.EventDay, statuses []platformAnalyticsRepo.EventStatusCount,
	rows []platformAnalyticsRepo.EventRow, total int64, upcoming []platformAnalyticsRepo.UpcomingEvent) EventsView {
	view := EventsView{
		Period:      period,
		Series:      make([]EventDayView, 0, len(series)),
		Statuses:    make([]EventStatusView, 0, len(eventStatusOrder)),
		Events:      make([]EventRowView, 0, len(rows)),
		EventsTotal: total,
		EventsLimit: eventsTableLimit,
		Upcoming:    make([]UpcomingEventView, 0, len(upcoming)),
	}
	for _, day := range series {
		view.Series = append(view.Series, EventDayView{Day: day.Day, EventsCreated: day.EventsCreated, EventsStarted: day.EventsStarted, Registrations: day.Registrations})
		view.Totals.Created += day.EventsCreated
		view.Totals.Started += day.EventsStarted
		view.Totals.Registrations += day.Registrations
	}
	counts := make(map[int16]int64, len(statuses))
	for _, s := range statuses {
		counts[s.Status] = s.Events
	}
	for _, status := range eventStatusOrder {
		n := counts[int16(status)]
		view.Statuses = append(view.Statuses, EventStatusView{Status: EventStatusName(int16(status)), Events: n})
		view.Totals.Events += n
	}
	for _, row := range rows {
		view.Events = append(view.Events, eventRowView(row))
	}
	for _, next := range upcoming {
		view.Upcoming = append(view.Upcoming, UpcomingEventView{
			ID: next.ID, Tag: next.Tag, Name: next.Name, StartAt: next.StartAt, Published: next.Published, Registrations: next.Registrations,
		})
	}
	return view
}

func eventRowView(row platformAnalyticsRepo.EventRow) EventRowView {
	v := EventRowView{
		ID: row.ID, Tag: row.Tag, Name: row.Name, Status: EventStatusName(row.Status), StartAt: row.StartAt, FinishAt: row.FinishAt,
		Participants: row.Participants, Teams: row.Teams, Solves: row.Solves, TeamsSolved: row.TeamsSolved,
	}
	if !row.Configured {
		v.StartAt = time.Time{}
	}
	if row.Teams > 0 {
		v.CompletionRate = float64(row.TeamsSolved) / float64(row.Teams)
	}
	if row.Configured && row.FinishAt != nil && row.FinishAt.After(row.StartAt) {
		secs := int64(row.FinishAt.Sub(row.StartAt).Seconds())
		v.DurationSeconds = &secs
	}
	return v
}
