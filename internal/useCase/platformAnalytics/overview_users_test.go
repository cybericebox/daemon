package platformAnalytics

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
)

// ouStore serves the overview and users ports; every other section's calls
// panic on the nil embedded interface.
type ouStore struct {
	Store
	calls         atomic.Int32
	window        platformAnalyticsRepo.OverviewWindow
	usersDays     []platformAnalyticsRepo.OverviewUsersDay
	activeDays    []platformAnalyticsRepo.UsersActiveDay
	people        []platformAnalyticsRepo.UsersPerson
	peopleLimit   int32
	failActivity  error
	blockedStatus string
}

func (s *ouStore) OverviewUsers(_ context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewUsers, error) {
	s.calls.Add(1)
	s.window = w
	return platformAnalyticsRepo.OverviewUsers{Total: 100, New: 12, NewPrev: 10}, nil
}

func (s *ouStore) ActiveUsers(context.Context, platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewActiveUsers, error) {
	return platformAnalyticsRepo.OverviewActiveUsers{Active: 40, ActivePrev: 50}, nil
}

func (s *ouStore) OverviewEvents(context.Context, platformAnalyticsRepo.OverviewWindow, time.Time) (platformAnalyticsRepo.OverviewEvents, error) {
	return platformAnalyticsRepo.OverviewEvents{Draft: 1, Published: 2, Running: 3, Finished: 4, Archived: 5, Total: 15, New: 2, NewPrev: 1}, nil
}

func (s *ouStore) OverviewParticipants(context.Context, platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewParticipants, error) {
	return platformAnalyticsRepo.OverviewParticipants{Registered: 30, RegisteredPrev: 20, Approved: 25, ApprovedPrev: 20}, nil
}

func (s *ouStore) OverviewActivity(context.Context, platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewActivity, error) {
	return platformAnalyticsRepo.OverviewActivity{Attempts: 500, AttemptsPrev: 400, Solves: 90, SolvesPrev: 100}, nil
}

func (s *ouStore) OverviewMail(context.Context, platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewMail, error) {
	return platformAnalyticsRepo.OverviewMail{Sent: 70, SentPrev: 60, Failed: 3, FailedPrev: 0}, nil
}

func (s *ouStore) OverviewStands(context.Context, platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewStands, error) {
	return platformAnalyticsRepo.OverviewStands{Ready: 8, Creating: 1, FailedNow: 2, Failures: 5, FailuresPrev: 4}, nil
}

func (s *ouStore) OverviewUsersDays(context.Context, time.Time, time.Time) ([]platformAnalyticsRepo.OverviewUsersDay, error) {
	return s.usersDays, nil
}

func (s *ouStore) OverviewActivityDays(_ context.Context, from, _ time.Time) ([]platformAnalyticsRepo.OverviewActivityDay, error) {
	if s.failActivity != nil {
		return nil, s.failActivity
	}
	return []platformAnalyticsRepo.OverviewActivityDay{{Day: from}, {Day: from.Add(24 * time.Hour), Attempts: 4, Solves: 1}}, nil
}

func (s *ouStore) OverviewMailDays(_ context.Context, from, _ time.Time) ([]platformAnalyticsRepo.OverviewMailDay, error) {
	return []platformAnalyticsRepo.OverviewMailDay{{Day: from, Sent: 2}}, nil
}

func (s *ouStore) UsersByRole(context.Context) ([]platformAnalyticsRepo.UsersRoleCount, error) {
	return []platformAnalyticsRepo.UsersRoleCount{{Role: "user", Count: 90}, {Role: "super_admin", Count: 1}}, nil
}

func (s *ouStore) UsersBlocked(_ context.Context, status string) (int64, error) {
	s.blockedStatus = status
	return 4, nil
}

func (s *ouStore) UsersActiveDays(context.Context, time.Time, time.Time) ([]platformAnalyticsRepo.UsersActiveDay, error) {
	return s.activeDays, nil
}

func (s *ouStore) UsersMethods(context.Context, time.Time, time.Time) (platformAnalyticsRepo.UsersMethods, error) {
	return platformAnalyticsRepo.UsersMethods{PasswordOnly: 60, ProviderOnly: 25, Both: 10, None: 5, ProviderOnlyNew: 3}, nil
}

func (s *ouStore) UsersRetention(context.Context, time.Time) (platformAnalyticsRepo.UsersRetention, error) {
	return platformAnalyticsRepo.UsersRetention{One: 20, Two: 5, ThreePlus: 2, Never: 73}, nil
}

func (s *ouStore) UsersPeople(_ context.Context, limit int32) ([]platformAnalyticsRepo.UsersPerson, error) {
	s.peopleLimit = limit
	return s.people, nil
}

var ouNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func ouUseCase(store Store) *PlatformAnalyticsUseCase {
	return New(Dependencies{Store: store, Now: func() time.Time { return ouNow }})
}

func TestOverviewComparesWithThePreviousPeriod(t *testing.T) {
	store := &ouStore{}
	uc := ouUseCase(store)
	from := ouNow.AddDate(0, 0, -7)
	view, err := uc.GetOverview(context.Background(), &from, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The window is the 7 UTC days ending with today; the previous one is the 7 days before.
	if view.Period.All || view.Period.Previous == nil || !view.Period.Previous.To.Equal(view.Period.From) {
		t.Fatalf("period = %+v", view.Period)
	}
	if got := view.Period.To.Sub(view.Period.From); got != 8*24*time.Hour {
		t.Fatalf("window = %v", got)
	}
	if !store.window.PrevTo.Equal(store.window.From) || store.window.PrevFrom.After(store.window.From) {
		t.Fatalf("store window = %+v", store.window)
	}
	if view.Users.Total != 100 || view.Users.New.Value != 12 || view.Users.New.Previous == nil || *view.Users.New.Previous != 10 {
		t.Fatalf("users = %+v", view.Users)
	}
	if view.Users.Active.Value != 40 || *view.Users.Active.Previous != 50 {
		t.Fatalf("active = %+v", view.Users.Active)
	}
	if view.Events.Running != 3 || view.Events.Total != 15 || *view.Events.New.Previous != 1 {
		t.Fatalf("events = %+v", view.Events)
	}
	if view.Activity.Attempts.Value != 500 || *view.Activity.Solves.Previous != 100 || *view.Mail.Failed.Previous != 0 || *view.Stands.Failures.Previous != 4 {
		t.Fatalf("activity/mail/stands = %+v %+v %+v", view.Activity, view.Mail, view.Stands)
	}
	if view.Stands.Ready != 8 || view.Stands.Failed != 2 {
		t.Fatalf("stands = %+v", view.Stands)
	}
	// A bounded period keeps every day of the series, empty ones included.
	if len(view.Series.Activity) != 2 || len(view.Series.Mail) != 1 {
		t.Fatalf("series = %+v", view.Series)
	}
}

func TestOverviewAllTimeHasNoPreviousAndTrimsEmptyLeadingDays(t *testing.T) {
	store := &ouStore{usersDays: []platformAnalyticsRepo.OverviewUsersDay{{Day: ouNow.AddDate(0, 0, -3)}, {Day: ouNow.AddDate(0, 0, -2), New: 2}, {Day: ouNow.AddDate(0, 0, -1)}}}
	view, err := ouUseCase(store).GetOverview(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Period.All || view.Period.Previous != nil || view.Users.New.Previous != nil || view.Mail.Sent.Previous != nil || view.Stands.Failures.Previous != nil {
		t.Fatalf("all time must have no previous period: %+v", view)
	}
	if len(view.Series.NewUsers) != 2 || view.Series.NewUsers[0].New != 2 {
		t.Fatalf("new users = %+v", view.Series.NewUsers)
	}
	// The empty previous window is passed to the store as an empty range.
	if !store.window.PrevFrom.Equal(store.window.PrevTo) {
		t.Fatalf("store window = %+v", store.window)
	}
	// The activity series starts with an empty day: trimmed.
	if len(view.Series.Activity) != 1 || view.Series.Activity[0].Attempts != 4 {
		t.Fatalf("activity = %+v", view.Series.Activity)
	}
}

func TestOverviewIsCachedPerPeriod(t *testing.T) {
	store := &ouStore{}
	uc := ouUseCase(store)
	from := ouNow.AddDate(0, 0, -30)
	for i := 0; i < 3; i++ {
		if _, err := uc.GetOverview(context.Background(), &from, nil); err != nil {
			t.Fatal(err)
		}
	}
	if store.calls.Load() != 1 {
		t.Fatalf("loads = %d, want 1", store.calls.Load())
	}
	if _, err := uc.GetOverview(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if store.calls.Load() != 2 {
		t.Fatalf("a different period must load again, loads = %d", store.calls.Load())
	}
}

func TestOverviewRejectsAnInvalidPeriodAndWrapsStoreErrors(t *testing.T) {
	uc := ouUseCase(&ouStore{})
	from, to := ouNow, ouNow.AddDate(0, 0, -1)
	if _, err := uc.GetOverview(context.Background(), &from, &to); err == nil {
		t.Fatal("from after to must fail")
	}
	failing := ouUseCase(&ouStore{failActivity: errors.New("boom")})
	if _, err := failing.GetOverview(context.Background(), nil, nil); err == nil {
		t.Fatal("a store error must surface")
	}
}

func TestUsersReportComposesTheStatsAndTheAggregates(t *testing.T) {
	store := &ouStore{activeDays: []platformAnalyticsRepo.UsersActiveDay{{Day: ouNow.AddDate(0, 0, -1), DAU: 10, WAU: 20}, {Day: ouNow, DAU: 20, WAU: 25}}}
	from := ouNow.AddDate(0, 0, -30)
	view, err := ouUseCase(store).GetUsers(context.Background(), &from, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 100 || view.Blocked != 4 || store.blockedStatus != "blocked" || len(view.ByRole) != 2 {
		t.Fatalf("stats = %+v (status %q)", view, store.blockedStatus)
	}
	if view.New.Value != 12 || *view.New.Previous != 10 || view.Active.Value != 40 {
		t.Fatalf("new/active = %+v %+v", view.New, view.Active)
	}
	if view.AvgDailyActive != 15 || len(view.ActiveByDay) != 2 {
		t.Fatalf("active by day = %+v avg %v", view.ActiveByDay, view.AvgDailyActive)
	}
	// The method groups come in a fixed order and are exclusive.
	wantMethods := []UsersMethodView{
		{Method: UsersMethodPassword, Total: 60}, {Method: UsersMethodGoogle, Total: 25, New: 3},
		{Method: UsersMethodBoth, Total: 10}, {Method: UsersMethodNone, Total: 5},
	}
	if len(view.Methods) != 4 {
		t.Fatalf("methods = %+v", view.Methods)
	}
	for i, want := range wantMethods {
		if view.Methods[i] != want {
			t.Fatalf("method %d = %+v, want %+v", i, view.Methods[i], want)
		}
	}
	if view.Retention != (UsersRetentionView{One: 20, Two: 5, ThreePlus: 2, Never: 73}) {
		t.Fatalf("retention = %+v", view.Retention)
	}
}

func TestUsersPeopleIsBoundedAndJoinsTheName(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	store := &ouStore{people: []platformAnalyticsRepo.UsersPerson{{ID: id, FirstName: "Ada", LastName: "Lovelace", Email: "ada@example.test", Role: "user", EventsJoined: 4, Solves: 9}, {ID: uuid.Must(uuid.NewV7()), FirstName: "", LastName: "Solo", Email: "solo@example.test"}}}
	rows, err := ouUseCase(store).GetUsersPeople(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if store.peopleLimit != UsersPeopleLimit || UsersPeopleLimit != 100 {
		t.Fatalf("limit = %d", store.peopleLimit)
	}
	if len(rows) != 2 || rows[0].Name != "Ada Lovelace" || rows[0].EventsJoined != 4 || rows[0].Solves != 9 || rows[1].Name != "Solo" {
		t.Fatalf("rows = %+v", rows)
	}
}
