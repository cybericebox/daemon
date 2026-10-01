package eventAnalytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// Default (empty) answers of the stands, integrity and report reads, so the
// shared fakeStore satisfies the ports; the tests below override them.
func (s *fakeStore) StandTeams(context.Context, uuid.UUID) ([]eventAnalyticsRepo.StandTeam, error) {
	return nil, nil
}
func (s *fakeStore) StandTransitions(context.Context, uuid.UUID) ([]eventAnalyticsModel.StandTransition, error) {
	return nil, nil
}
func (s *fakeStore) StandResources(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.StandResources, error) {
	return nil, nil
}
func (s *fakeStore) VPNUsage(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.VPNUsage, error) {
	return nil, nil
}
func (s *fakeStore) IntegrityFacts(context.Context, uuid.UUID) (eventAnalyticsModel.IntegrityFacts, error) {
	return eventAnalyticsModel.IntegrityFacts{}, nil
}
func (s *fakeStore) SolveReviews(context.Context, uuid.UUID) ([]eventAnalyticsRepo.SolveReview, error) {
	return nil, nil
}
func (s *fakeStore) SaveSolveReview(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, time.Time) (bool, error) {
	return false, nil
}
func (s *fakeStore) DeleteSolveReview(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (s *fakeStore) Dismissals(context.Context, uuid.UUID) ([]eventAnalyticsRepo.IntegrityDismissal, error) {
	return nil, nil
}
func (s *fakeStore) SaveDismissal(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.DismissScope, eventAnalyticsModel.IntegrityKind, string, string, uuid.UUID, uuid.UUID, time.Time) (bool, error) {
	return false, nil
}
func (s *fakeStore) DeleteDismissal(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (s *fakeStore) ReportRanking(context.Context, uuid.UUID) ([]eventAnalyticsRepo.ReportTeam, error) {
	return nil, nil
}
func (s *fakeStore) ReportTasks(context.Context, uuid.UUID) ([]eventAnalyticsRepo.ReportTask, error) {
	return nil, nil
}
func (s *fakeStore) ReportFunnel(context.Context, uuid.UUID) (eventAnalyticsRepo.ReportFunnel, error) {
	return eventAnalyticsRepo.ReportFunnel{}, nil
}

type sectionsStore struct {
	*fakeStore
	teams       []eventAnalyticsRepo.StandTeam
	transitions []eventAnalyticsModel.StandTransition
	resources   []eventAnalyticsRepo.StandResources
	vpn         []eventAnalyticsRepo.VPNUsage
	facts       eventAnalyticsModel.IntegrityFacts
	ranking     []eventAnalyticsRepo.ReportTeam
	tasks       []eventAnalyticsRepo.ReportTask
	funnel      eventAnalyticsRepo.ReportFunnel
	factReads   int
	usage       usageFixture
}

func (s *sectionsStore) StandTeams(context.Context, uuid.UUID) ([]eventAnalyticsRepo.StandTeam, error) {
	return s.teams, nil
}
func (s *sectionsStore) StandTransitions(context.Context, uuid.UUID) ([]eventAnalyticsModel.StandTransition, error) {
	return s.transitions, nil
}
func (s *sectionsStore) StandResources(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.StandResources, error) {
	return s.resources, nil
}
func (s *sectionsStore) VPNUsage(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.VPNUsage, error) {
	return s.vpn, nil
}
func (s *sectionsStore) IntegrityFacts(context.Context, uuid.UUID) (eventAnalyticsModel.IntegrityFacts, error) {
	s.factReads++
	return s.facts, nil
}
func (s *sectionsStore) ReportRanking(context.Context, uuid.UUID) ([]eventAnalyticsRepo.ReportTeam, error) {
	return s.ranking, nil
}
func (s *sectionsStore) ReportTasks(context.Context, uuid.UUID) ([]eventAnalyticsRepo.ReportTask, error) {
	return s.tasks, nil
}
func (s *sectionsStore) ReportFunnel(context.Context, uuid.UUID) (eventAnalyticsRepo.ReportFunnel, error) {
	return s.funnel, nil
}

func TestGetEventAnalyticsStands_NoInfrastructureIsUnavailable(t *testing.T) {
	clock := now
	event := runningEvent()
	uc := newUC(&fakeStore{}, event, nil, &clock)

	v, err := uc.GetEventAnalyticsStands(context.Background(), event.ID, nil, nil)
	require.NoError(t, err)
	require.False(t, v.Available)
	require.Empty(t, v.Teams)
}

func TestGetEventAnalyticsStands_SummarizesTeams(t *testing.T) {
	clock := now
	event := runningEvent()
	event.InfrastructureAllowed = true
	a, b, c := uuid.UUID{15: 1}, uuid.UUID{15: 2}, uuid.UUID{15: 3}
	at := func(s int) time.Time { return time.Date(2026, 9, 29, 12, 0, s, 0, time.UTC) }
	store := &sectionsStore{
		fakeStore: &fakeStore{},
		teams: []eventAnalyticsRepo.StandTeam{
			{TeamID: a, TeamName: "A", Status: 2, MemberCount: 3},
			{TeamID: b, TeamName: "B", Status: 3, MemberCount: 2},
			{TeamID: c, TeamName: "C", Status: 0, MemberCount: 1},
		},
		transitions: []eventAnalyticsModel.StandTransition{
			{TeamID: a, Source: eventAnalyticsModel.StandSourceStand, Generation: 1, To: 1, At: at(0)},
			{TeamID: a, Source: eventAnalyticsModel.StandSourceStand, Generation: 1, To: 2, At: at(60)},
			{TeamID: b, Source: eventAnalyticsModel.StandSourceStand, Generation: 1, To: 1, At: at(0)},
			{TeamID: b, Source: eventAnalyticsModel.StandSourceStand, Generation: 1, To: 3, Reason: "no capacity", At: at(20)},
		},
		resources: []eventAnalyticsRepo.StandResources{{TeamID: a, Devices: 2, PeakCPUMillis: 500, PeakMemoryBytes: 1 << 30, Restarts: 3}},
		vpn:       []eventAnalyticsRepo.VPNUsage{{TeamID: a, Sessions: 4, Users: 2, Seconds: 600, RxBytes: 10, TxBytes: 20, LastAt: at(90)}},
	}
	uc := newUCWithStore(store, event, &clock)

	v, err := uc.GetEventAnalyticsStands(context.Background(), event.ID, nil, nil)
	require.NoError(t, err)
	require.True(t, v.Available)
	require.EqualValues(t, 3, v.Summary.Teams)
	require.EqualValues(t, 1, v.Summary.Ready)
	require.EqualValues(t, 1, v.Summary.Failed)
	require.EqualValues(t, 1, v.Summary.NotDeployed)
	require.EqualValues(t, 60, *v.Summary.DeployAvg)
	require.EqualValues(t, 1, v.Summary.Failures)
	require.EqualValues(t, 1, v.Summary.Unresolved)
	require.EqualValues(t, 3, v.Summary.Restarts)
	require.EqualValues(t, 1, v.Summary.VPNTeams)

	require.Equal(t, "ready", v.Teams[0].Status)
	require.EqualValues(t, 60, *v.Teams[0].DeploySeconds)
	require.EqualValues(t, 3, v.Teams[0].VPN.Members)
	require.Equal(t, "failed", v.Teams[1].Status)
	require.Equal(t, "no capacity", v.Teams[1].Failures[0].Reason)
	require.Nil(t, v.Teams[1].Failures[0].RecoveredAt)
}

func TestGetEventAnalyticsReport_OnlyAfterTheFinish(t *testing.T) {
	clock := now
	event := runningEvent()
	store := &sectionsStore{fakeStore: &fakeStore{}}
	uc := newUCWithStore(store, event, &clock)

	v, err := uc.GetEventAnalyticsReport(context.Background(), event.ID)
	require.NoError(t, err)
	require.False(t, v.Available)
	require.NotNil(t, v.FinishAt)

	clock = event.Lifecycle.FinishAt.Add(time.Hour)
	last := clock.Add(-time.Hour)
	store.fakeStore.overview = eventAnalyticsRepo.Overview{ParticipantsRegistered: 9, ParticipantsApproved: 8, TeamsTotal: 4, TeamsAdmitted: 3}
	store.ranking = []eventAnalyticsRepo.ReportTeam{
		{Name: "A", Points: 300, LastSolveAt: &last},
		{Name: "B", Points: 200, LastSolveAt: &last},
		{Name: "C", Points: 200, LastSolveAt: &last},
		{Name: "D"},
	}
	store.tasks = []eventAnalyticsRepo.ReportTask{{Name: "Web", TeamsAttempted: 4, Solves: 1}, {Name: "Idle"}}
	store.funnel = eventAnalyticsRepo.ReportFunnel{ParticipantsOpened: 7, ParticipantsAttempted: 6, TeamsAttempted: 3, TeamsSolved: 2}

	v, err = uc.GetEventAnalyticsReport(context.Background(), event.ID)
	require.NoError(t, err)
	require.True(t, v.Available)
	require.EqualValues(t, 2, v.Tasks)
	require.Equal(t, []int{1, 2, 2, 4}, []int{v.Ranking[0].Rank, v.Ranking[1].Rank, v.Ranking[2].Rank, v.Ranking[3].Rank})
	require.InDelta(t, 0.25, v.TaskRows[0].SolveRate, 1e-9)
	require.Zero(t, v.TaskRows[1].SolveRate)
	require.EqualValues(t, 7, v.ParticipantFunnel[2].Count)
	require.EqualValues(t, 2, v.TeamFunnel[3].Count)
	require.Nil(t, v.Stands)
}

func newUCWithStore(store eventAnalyticsStore, event eventModel.Event, clock *time.Time) *eventAnalytics.EventAnalyticsUseCase {
	return eventAnalytics.New(eventAnalytics.Dependencies{
		Store: store, Events: fakeEvents{event}, Configs: fakeConfigs{}, Memberships: fakeMemberships{},
		Now: func() time.Time { return *clock },
	})
}

type eventAnalyticsStore = eventAnalytics.Store
