package eventAnalytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// snapshotStore serves the scoreboard and the task table to the overview.
type snapshotStore struct {
	*fakeStore
	teams      []eventAnalyticsRepo.RankedTeam
	challenges []eventAnalyticsRepo.Challenge
	stats      []eventAnalyticsRepo.TaskStats
}

func (s *snapshotStore) RankedTeams(context.Context, uuid.UUID) ([]eventAnalyticsRepo.RankedTeam, error) {
	return s.teams, nil
}
func (s *snapshotStore) Challenges(context.Context, uuid.UUID) ([]eventAnalyticsRepo.Challenge, error) {
	return s.challenges, nil
}
func (s *snapshotStore) TaskStats(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.TaskStats, error) {
	return s.stats, nil
}

func TestGetEventAnalyticsOverview_Snapshots(t *testing.T) {
	clock := now
	event := runningEvent()
	blood := now.Add(-time.Hour)
	fake := &fakeStore{}
	fake.dispatches = []eventAnalyticsRepo.DispatchCount{
		{Type: "invite", Channel: "email", Status: "done", Targets: 8},
		{Type: "invite", Channel: "email", Status: "error", Targets: 2},
		{Type: "invite", Channel: "in_app", Status: "done", Targets: 50},
	}
	store := &snapshotStore{
		fakeStore: fake,
		teams: []eventAnalyticsRepo.RankedTeam{
			{ID: id(1), Name: "Blue", Points: 300, Solved: 3, Admitted: true},
			{ID: id(2), Name: "Hidden", Points: 900, Solved: 9, Admitted: true, Hidden: true},
			{ID: id(3), Name: "Red", Points: 250, Solved: 2, Admitted: true},
			{ID: id(4), Name: "Green", Points: 0, Solved: 0, Admitted: true},
			{ID: id(5), Name: "Draft", Points: 50, Solved: 1, Admitted: false},
		},
		challenges: []eventAnalyticsRepo.Challenge{{ID: id(11), Name: "Web"}, {ID: id(12), Name: "Pwn"}, {ID: id(13), Name: "Crypto"}, {ID: id(14), Name: "Misc"}},
		stats: []eventAnalyticsRepo.TaskStats{
			{ChallengeID: id(11), Solves: 4, FirstBloodAt: &blood},
			{ChallengeID: id(12), Solves: 1, FirstBloodAt: &blood},
			{ChallengeID: id(13), Solves: 0},
		},
	}
	v, err := newUCWithStore(store, event, &clock).GetEventAnalyticsOverview(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Hidden and not admitted teams stay off the board; the gap is to the first place.
	if v.RankedTeams != 3 || len(v.Leaders) != 3 {
		t.Fatalf("leaders: %+v", v.Leaders)
	}
	if v.Leaders[0].Name != "Blue" || v.Leaders[0].Gap != 0 || v.Leaders[1].Name != "Red" || v.Leaders[1].Gap != 50 || v.Leaders[1].Rank != 2 || v.Leaders[2].Gap != 300 {
		t.Fatalf("leaders: %+v", v.Leaders)
	}

	if v.Tasks.Total != 4 || v.Tasks.Unsolved != 2 || v.Tasks.FirstBloods != 2 {
		t.Fatalf("tasks: %+v", v.Tasks)
	}
	if v.Tasks.MostSolved == nil || v.Tasks.MostSolved.Name != "Web" || v.Tasks.LeastSolved == nil || v.Tasks.LeastSolved.Name != "Pwn" {
		t.Fatalf("most/least: %+v %+v", v.Tasks.MostSolved, v.Tasks.LeastSolved)
	}

	// Four admitted teams (the hidden one counts, the draft one does not): 3+9+2+0 solves, 3 of them solving.
	if v.Engagement.Teams != 4 || v.Engagement.TeamsSolving != 3 || v.Engagement.AvgSolves != 3.5 {
		t.Fatalf("engagement: %+v", v.Engagement)
	}

	// Only mail counts, and the window is the last 24 hours.
	if v.Comms.EmailSent != 8 || v.Comms.EmailFailed != 2 || !v.Comms.Since.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("comms: %+v", v.Comms)
	}
}

func TestGetEventAnalyticsOverview_SnapshotsOfAnEmptyEvent(t *testing.T) {
	clock := now
	event := runningEvent()
	store := &snapshotStore{fakeStore: &fakeStore{}}
	v, err := newUCWithStore(store, event, &clock).GetEventAnalyticsOverview(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Leaders) != 0 || v.Tasks.Total != 0 || v.Tasks.MostSolved != nil || v.Tasks.LeastSolved != nil || v.Engagement.AvgSolves != 0 || v.Comms.EmailSent != 0 {
		t.Fatalf("empty event: %+v", v)
	}
}
