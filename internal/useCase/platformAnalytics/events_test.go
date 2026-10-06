package platformAnalytics

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
)

var catalogTestNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// catalogFakeStore serves the events and tasks ports; a call counter shows the cache.
type catalogFakeStore struct {
	Store

	series   []platformAnalyticsRepo.EventDay
	statuses []platformAnalyticsRepo.EventStatusCount
	events   []platformAnalyticsRepo.EventRow
	upcoming []platformAnalyticsRepo.UpcomingEvent
	tasks    []platformAnalyticsRepo.CatalogTask
	cats     []string
	calls    int

	category, level string
	seriesFrom      time.Time
	seriesTo        time.Time
}

func (f *catalogFakeStore) EventSeries(_ context.Context, from, to time.Time) ([]platformAnalyticsRepo.EventDay, error) {
	f.calls++
	f.seriesFrom, f.seriesTo = from, to
	return f.series, nil
}
func (f *catalogFakeStore) EventStatuses(context.Context, time.Time, time.Time, time.Time) ([]platformAnalyticsRepo.EventStatusCount, error) {
	return f.statuses, nil
}
func (f *catalogFakeStore) Events(context.Context, time.Time, time.Time, time.Time, int32) ([]platformAnalyticsRepo.EventRow, int64, error) {
	return f.events, int64(len(f.events)), nil
}
func (f *catalogFakeStore) UpcomingEvents(context.Context, time.Time, int32) ([]platformAnalyticsRepo.UpcomingEvent, error) {
	return f.upcoming, nil
}
func (f *catalogFakeStore) TaskCatalog(_ context.Context, _, _ time.Time, category, level string, _ int32) ([]platformAnalyticsRepo.CatalogTask, int64, error) {
	f.calls++
	f.category, f.level = category, level
	return f.tasks, int64(len(f.tasks)), nil
}
func (f *catalogFakeStore) TaskCategories(context.Context, time.Time, time.Time) ([]string, error) {
	return f.cats, nil
}

func newCatalogUseCase(store *catalogFakeStore) *PlatformAnalyticsUseCase {
	return New(Dependencies{Store: store, Now: func() time.Time { return catalogTestNow }})
}

func TestGetEvents(t *testing.T) {
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	finish := day.Add(10 * time.Hour)
	store := &catalogFakeStore{
		series: []platformAnalyticsRepo.EventDay{
			{Day: day, EventsCreated: 1, EventsStarted: 2, Registrations: 10},
			{Day: day.AddDate(0, 0, 1), EventsCreated: 0, EventsStarted: 1, Registrations: 5},
		},
		statuses: []platformAnalyticsRepo.EventStatusCount{{Status: 2, Events: 1}, {Status: 3, Events: 4}},
		events: []platformAnalyticsRepo.EventRow{
			{ID: uuid.Must(uuid.NewV7()), Tag: "ctf", Name: "CTF", Status: 3, Configured: true, StartAt: day, FinishAt: &finish, Participants: 20, Teams: 8, Solves: 30, TeamsSolved: 6},
			{ID: uuid.Must(uuid.NewV7()), Tag: "draft", Name: "Draft", Status: 0},
		},
		upcoming: []platformAnalyticsRepo.UpcomingEvent{{Tag: "next", Name: "Next", StartAt: catalogTestNow.Add(time.Hour), Published: true, Registrations: 3}},
	}
	u := newCatalogUseCase(store)
	from := day.AddDate(0, 0, -7)
	v, err := u.GetEvents(context.Background(), &from, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Totals.Created != 1 || v.Totals.Started != 3 || v.Totals.Registrations != 15 || v.Totals.Events != 5 {
		t.Fatalf("totals = %+v", v.Totals)
	}
	if len(v.Statuses) != 5 || v.Statuses[2].Status != "started" || v.Statuses[2].Events != 1 || v.Statuses[3].Status != "finished" || v.Statuses[3].Events != 4 ||
		v.Statuses[0].Status != "not_published" || v.Statuses[4].Events != 0 {
		t.Fatalf("statuses = %+v", v.Statuses)
	}
	first, draft := v.Events[0], v.Events[1]
	if first.CompletionRate != 0.75 || first.DurationSeconds == nil || *first.DurationSeconds != 36000 || first.Status != "finished" {
		t.Fatalf("event row = %+v", first)
	}
	if draft.CompletionRate != 0 || draft.DurationSeconds != nil || !draft.StartAt.IsZero() {
		t.Fatalf("draft row = %+v", draft)
	}
	if len(v.Upcoming) != 1 || v.Upcoming[0].Registrations != 3 || v.EventsLimit != eventsTableLimit {
		t.Fatalf("upcoming = %+v", v.Upcoming)
	}
	// The period is UTC-day aligned and includes today.
	if !store.seriesFrom.Equal(day.AddDate(0, 0, -7)) || !store.seriesTo.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("period = %v .. %v", store.seriesFrom, store.seriesTo)
	}
	// A second read of the same period is served from the cache.
	if _, err = u.GetEvents(context.Background(), &from, nil); err != nil || store.calls != 1 {
		t.Fatalf("calls = %d, err = %v", store.calls, err)
	}
}

func TestGetEventsInvalidPeriod(t *testing.T) {
	u := newCatalogUseCase(&catalogFakeStore{})
	from := catalogTestNow
	to := catalogTestNow.Add(-time.Hour)
	if _, err := u.GetEvents(context.Background(), &from, &to); err == nil {
		t.Fatal("from after to must fail")
	}
}
