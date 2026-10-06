package eventAnalytics_test

import (
	"context"
	"errors"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

var now = time.Date(2026, 9, 29, 12, 2, 0, 0, time.UTC)

type fakeStore struct {
	participantFake
	mu            sync.Mutex
	overviewCalls int
	overview      eventAnalyticsRepo.Overview
	series        []eventAnalyticsRepo.SeriesPoint
	feed          []eventAnalyticsRepo.FeedItem
	state         eventAnalyticsRepo.RollupState
	activeSince   time.Time

	candidates []eventAnalyticsRepo.RollupCandidate
	refreshed  []uuid.UUID
	marked     map[uuid.UUID]*time.Time
	failBucket uuid.UUID

	samples   [][]eventAnalyticsModel.VPNSample
	cursors   []eventAnalyticsRepo.VPNCursor
	known     []eventAnalyticsModel.VPNSession
	saved     []eventAnalyticsModel.VPNSession
	setCursor []eventAnalyticsRepo.VPNCursor
	vpnFinal  *time.Time
}

func (s *fakeStore) RollupCandidates(context.Context, time.Time, int32) ([]eventAnalyticsRepo.RollupCandidate, error) {
	return s.candidates, nil
}
func (s *fakeStore) RefreshBuckets(_ context.Context, eventID uuid.UUID) error {
	if eventID == s.failBucket {
		return errors.New("boom")
	}
	s.refreshed = append(s.refreshed, eventID)
	return nil
}
func (s *fakeStore) MarkBucketsRefreshed(_ context.Context, eventID uuid.UUID, _ time.Time, _ int64, finalizedAt *time.Time) error {
	if s.marked == nil {
		s.marked = map[uuid.UUID]*time.Time{}
	}
	s.marked[eventID] = finalizedAt
	return nil
}
func (s *fakeStore) VPNSamples(_ context.Context, _ uuid.UUID, after eventAnalyticsRepo.VPNCursor, _ int32) ([]eventAnalyticsModel.VPNSample, eventAnalyticsRepo.VPNCursor, int, error) {
	if len(s.samples) == 0 {
		return nil, after, 0, nil
	}
	batch := s.samples[0]
	s.samples = s.samples[1:]
	next := s.cursors[0]
	s.cursors = s.cursors[1:]
	return batch, next, len(batch), nil
}
func (s *fakeStore) OpenVPNSessions(context.Context, uuid.UUID, time.Time) ([]eventAnalyticsModel.VPNSession, error) {
	return s.known, nil
}
func (s *fakeStore) SaveVPNSessions(_ context.Context, _ uuid.UUID, changed []eventAnalyticsModel.VPNSession, _ []uuid.UUID) error {
	s.saved = append(s.saved, changed...)
	return nil
}
func (s *fakeStore) SetVPNCursor(_ context.Context, _ uuid.UUID, cursor eventAnalyticsRepo.VPNCursor, finalizedAt *time.Time) error {
	s.setCursor = append(s.setCursor, cursor)
	if finalizedAt != nil {
		s.vpnFinal = finalizedAt
	}
	return nil
}
func (s *fakeStore) Overview(_ context.Context, _ uuid.UUID, activeSince time.Time) (eventAnalyticsRepo.Overview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overviewCalls++
	s.activeSince = activeSince
	return s.overview, nil
}
func (s *fakeStore) Series(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.SeriesPoint, error) {
	return s.series, nil
}
func (s *fakeStore) Feed(context.Context, uuid.UUID, int32) ([]eventAnalyticsRepo.FeedItem, error) {
	return s.feed, nil
}
func (s *fakeStore) RollupState(context.Context, uuid.UUID) (eventAnalyticsRepo.RollupState, error) {
	return s.state, nil
}

type fakeEvents struct{ event eventModel.Event }

func (f fakeEvents) GetByID(_ context.Context, id uuid.UUID) (eventModel.Event, error) {
	if id != f.event.ID {
		return eventModel.Event{}, pgx.ErrNoRows
	}
	return f.event, nil
}

type fakeConfigs struct{ config eventConfigModel.EventConfig }

func (f fakeConfigs) Get(context.Context, uuid.UUID) (eventConfigModel.EventConfig, error) {
	return f.config, nil
}

type fakeMemberships map[uuid.UUID]eventManagerModel.Role

func (f fakeMemberships) Get(_ context.Context, eventID, userID uuid.UUID) (eventManagerModel.EventManager, error) {
	role, ok := f[userID]
	if !ok {
		return eventManagerModel.EventManager{}, pgx.ErrNoRows
	}
	return eventManagerModel.EventManager{EventID: eventID, UserID: userID, Role: role}, nil
}

func newUC(store *fakeStore, event eventModel.Event, members fakeMemberships, clock *time.Time) *eventAnalytics.EventAnalyticsUseCase {
	config := eventConfigModel.EventConfig{Results: eventConfigModel.ResultsSettings{FreezeEnabled: true, FreezeMinutes: 60}}
	n := byte(0)
	return eventAnalytics.New(eventAnalytics.Dependencies{
		Store: store, Events: fakeEvents{event}, Configs: fakeConfigs{config}, Memberships: members,
		Now:   func() time.Time { return *clock },
		NewID: func() uuid.UUID { n++; return uuid.UUID{15: n} },
	})
}

func runningEvent() eventModel.Event {
	finish := time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC)
	return eventModel.Event{ID: uuid.Must(uuid.NewV7()), Lifecycle: eventModel.Lifecycle{
		Configured: true, PublishAt: now.Add(-24 * time.Hour), StartAt: time.Date(2026, 9, 29, 11, 50, 0, 0, time.UTC), FinishAt: &finish,
	}}
}

func TestGetEventAnalyticsOverview_MapsCountersSeriesAndMarkers(t *testing.T) {
	clock := now
	event := runningEvent()
	refreshed := now.Add(-time.Minute)
	store := &fakeStore{
		overview: eventAnalyticsRepo.Overview{ParticipantsRegistered: 12, ParticipantsApproved: 10, ParticipantsActive: 4, TeamsTotal: 5, TeamsAdmitted: 3, Attempts: 40, AttemptsCorrect: 7, Solves: 6, StandsFailed: 1},
		series:   []eventAnalyticsRepo.SeriesPoint{{At: time.Date(2026, 9, 29, 11, 55, 0, 0, time.UTC), Attempts: 3, Solves: 1}},
		state:    eventAnalyticsRepo.RollupState{RefreshedAt: &refreshed},
	}
	uc := newUC(store, event, nil, &clock)

	v, err := uc.GetEventAnalyticsOverview(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Participants.Registered != 12 || v.Participants.Active != 4 || v.Teams.Incomplete != 2 || v.Correct != 7 || v.Stands.Failed != 1 {
		t.Fatalf("counters: %+v", v)
	}
	if !store.activeSince.Equal(now.Add(-15 * time.Minute)) {
		t.Fatalf("active window starts at %s", store.activeSince)
	}
	// 11:50 → 12:05: three buckets, the stored one in the middle.
	if len(v.Series) != 3 || v.Series[1].Attempts != 3 || v.Series[0].Attempts != 0 || v.Series[2].Solves != 0 {
		t.Fatalf("series: %+v", v.Series)
	}
	if !v.Period.From.Equal(event.Lifecycle.StartAt) || !v.Period.To.Equal(time.Date(2026, 9, 29, 12, 5, 0, 0, time.UTC)) {
		t.Fatalf("period: %+v", v.Period)
	}
	if v.Markers.FreezeAt == nil || !v.Markers.FreezeAt.Equal(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)) || v.Markers.FinishAt == nil {
		t.Fatalf("markers: %+v", v.Markers)
	}
	if v.RefreshedAt == nil || v.Final {
		t.Fatalf("rollup state: %+v %v", v.RefreshedAt, v.Final)
	}
}

// The feed keeps the store's order, marks a team-less moment without an ID
// and adds the freeze start only once the freeze has begun.
func TestGetEventAnalyticsOverview_FeedAddsTheFreezeStart(t *testing.T) {
	teamID := uuid.Must(uuid.NewV7())
	store := &fakeStore{feed: []eventAnalyticsRepo.FeedItem{
		{Kind: "first_blood", At: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), TeamID: teamID, TeamName: "Blue", ChallengeName: "Web 1"},
		{Kind: "team_created", At: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC), TeamName: "Red"},
	}}
	event := runningEvent()

	before := now
	v, err := newUC(store, event, nil, &before).GetEventAnalyticsOverview(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Feed) != 2 || v.Feed[0].Kind != "first_blood" || v.Feed[0].TeamID == nil || *v.Feed[0].TeamID != teamID || v.Feed[1].TeamID != nil {
		t.Fatalf("feed before the freeze: %+v", v.Feed)
	}

	frozen := time.Date(2026, 9, 29, 15, 30, 0, 0, time.UTC)
	v, err = newUC(store, event, nil, &frozen).GetEventAnalyticsOverview(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Feed) != 3 || v.Feed[0].Kind != "freeze_started" || !v.Feed[0].At.Equal(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("feed after the freeze: %+v", v.Feed)
	}
}

// Concurrent and repeated reads inside the TTL share one load; after the TTL
// the report is loaded again.
func TestGetEventAnalyticsOverview_SharedCache(t *testing.T) {
	clock := now
	event := runningEvent()
	store := &fakeStore{}
	uc := newUC(store, event, nil, &clock)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := uc.GetEventAnalyticsOverview(context.Background(), event.ID, nil, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if store.overviewCalls != 1 {
		t.Fatalf("loads inside the TTL = %d, want 1", store.overviewCalls)
	}
	clock = now.Add(11 * time.Second)
	if _, err := uc.GetEventAnalyticsOverview(context.Background(), event.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if store.overviewCalls != 2 {
		t.Fatalf("loads after the TTL = %d, want 2", store.overviewCalls)
	}
}

func TestGetEventAnalyticsOverview_Errors(t *testing.T) {
	clock := now
	event := runningEvent()
	uc := newUC(&fakeStore{}, event, nil, &clock)
	if _, err := uc.GetEventAnalyticsOverview(context.Background(), uuid.Must(uuid.NewV7()), nil, nil); !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("missing event: %v", err)
	}
	from, to := now, now.Add(-time.Hour)
	if _, err := uc.GetEventAnalyticsOverview(context.Background(), event.ID, &from, &to); !errors.Is(err, eventAnalyticsModel.ErrEventAnalyticsPeriodInvalid.Err()) {
		t.Fatalf("reversed period: %v", err)
	}
}

func TestRequireEventAnalytics(t *testing.T) {
	clock := now
	event := runningEvent()
	owner, reader, stranger := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := newUC(&fakeStore{}, event, fakeMemberships{owner: eventManagerModel.RoleOwner, reader: eventManagerModel.RoleViewer}, &clock)
	ctx := context.Background()
	check := func(user uuid.UUID, role rbac.Role, level eventAnalytics.Level) error {
		return uc.RequireEventAnalytics(ctx, event.ID, rbac.Claims{UserID: user, Role: role}, level)
	}

	if err := check(owner, rbac.RoleUser, eventAnalytics.LevelSensitive); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if err := check(reader, rbac.RoleUser, eventAnalytics.LevelSections); err != nil {
		t.Fatalf("read-only moderator, sections: %v", err)
	}
	if err := check(reader, rbac.RoleUser, eventAnalytics.LevelSensitive); !errors.Is(err, eventAnalyticsModel.ErrEventAnalyticsSensitiveForbidden.Err()) {
		t.Fatalf("read-only moderator, sensitive: %v", err)
	}
	if err := check(stranger, rbac.RoleUser, eventAnalytics.LevelSections); !errors.Is(err, eventAnalyticsModel.ErrEventAnalyticsForbidden.Err()) {
		t.Fatalf("stranger: %v", err)
	}
	if err := check(stranger, rbac.RoleAdmin, eventAnalytics.LevelSensitive); err != nil {
		t.Fatalf("platform admin: %v", err)
	}
}

// Buckets are rebuilt for events without a final rollup or with results
// changed since; an event past its finish (plus grace) is finalized. A
// failing event does not stop the others.
func TestRefreshEventAnalytics_Buckets(t *testing.T) {
	clock := now
	finished, running, final, changed, failing := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	longAgo := now.Add(-time.Hour)
	store := &fakeStore{
		candidates: []eventAnalyticsRepo.RollupCandidate{
			{EventID: running},
			{EventID: finished, FinishedAt: &longAgo},
			{EventID: final, FinishedAt: &longAgo, BucketsFinalized: true, Revision: 3, BucketsRevision: 3, InfrastructureAllowed: true},
			{EventID: changed, FinishedAt: &longAgo, BucketsFinalized: true, Revision: 4, BucketsRevision: 3},
			{EventID: failing},
		},
		failBucket: failing,
	}
	uc := newUC(store, runningEvent(), nil, &clock)

	if err := uc.RefreshEventAnalytics(context.Background()); err == nil {
		t.Fatal("a failing event must fail the pass")
	}
	if len(store.refreshed) != 3 || store.refreshed[0] != running || store.refreshed[1] != finished || store.refreshed[2] != changed {
		t.Fatalf("rebuilt: %v", store.refreshed)
	}
	if store.marked[running] != nil || store.marked[finished] == nil || store.marked[changed] == nil {
		t.Fatalf("finalization: %+v", store.marked)
	}
	if _, rebuilt := store.marked[final]; rebuilt {
		t.Fatal("a final event with unchanged results is not rebuilt")
	}
}

// New telemetry is folded into sessions and the cursor follows; once caught
// up after the event is final, the VPN rollup closes.
func TestRefreshEventAnalytics_VPNSessions(t *testing.T) {
	clock := now
	eventID, team := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	longAgo := now.Add(-time.Hour)
	client := labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7()))
	h := now.Add(-2 * time.Hour)
	cursor := eventAnalyticsRepo.VPNCursor{ReceivedAt: h, ID: uuid.Must(uuid.NewV7())}
	store := &fakeStore{
		candidates: []eventAnalyticsRepo.RollupCandidate{{EventID: eventID, FinishedAt: &longAgo, InfrastructureAllowed: true, BucketsFinalized: true}},
		samples:    [][]eventAnalyticsModel.VPNSample{{{TeamID: team, Client: client, ObservedAt: h, Handshake: h, Rx: 10, Tx: 1}}},
		cursors:    []eventAnalyticsRepo.VPNCursor{cursor},
	}
	uc := newUC(store, runningEvent(), nil, &clock)

	if err := uc.RefreshEventAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.saved[0].Client != client || store.saved[0].UserID == nil {
		t.Fatalf("sessions saved: %+v", store.saved)
	}
	if len(store.setCursor) != 2 || store.setCursor[0] != cursor || store.setCursor[1] != cursor || store.vpnFinal == nil {
		t.Fatalf("cursor moves then closes: %+v final=%v", store.setCursor, store.vpnFinal)
	}
}
