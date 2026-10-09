package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

func TestClosedRuntimeReadNeedsNoAgent(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	// A closed runtime remains readable even with no configured live agent.
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, InfrastructureCapability: standCapability{}})
	got, err := uc.GetOwnChallengeRuntime(context.Background(), f.eventID, user, set.challenges[0])
	if err != nil || got.Lab == nil || !got.Lab.LogicalClosed || got.Lab.RuntimeState != "closed" || got.Status.Ready || len(got.Status.Access) != 0 {
		t.Fatalf("closed runtime: %+v %v", got, err)
	}
	board, err := f.uc.ListOwnBoard(context.Background(), f.eventID, user)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, q := range board.Challenges {
		if q.EventExerciseID == got.Lab.EventExerciseID {
			count++
			if q.Lab == nil || q.Lab.ID != got.Lab.ID || q.Lab.Revision != got.Lab.Revision {
				t.Fatalf("question disagrees: %+v", q)
			}
		}
	}
	if count != 3 {
		t.Fatalf("shared questions=%d", count)
	}
}

func TestClosedLabCannotOpenLinkOrMutateDevice(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	sessions := &recordingSessions{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: f.agent, InfrastructureCapability: standCapability{}, LabSessions: sessions})
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	if link, err := uc.OpenOwnLabLink(context.Background(), f.eventID, user, set.challenges[0], "web", 80); err == nil || link.URL != "" || len(sessions.sessions) != 0 {
		t.Fatalf("closed link: %+v %v", link, err)
	}
	if err := uc.ResetStandDevice(context.Background(), f.eventID, f.blueID, set.challenges[0], "web"); err == nil {
		t.Fatal("closed reset accepted")
	}
	if err := uc.RescueStandDevice(context.Background(), f.eventID, f.blueID, set.challenges[1], "web", true); err == nil {
		t.Fatal("closed rescue accepted")
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	if len(f.agent.deviceCalls) != 0 {
		t.Fatal(f.agent.deviceCalls)
	}
}

func TestManagedSharedLabIsOneCanonicalRow(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	detail, err := f.uc.GetTeamStandDetail(context.Background(), f.eventID, f.blueID)
	if err != nil {
		t.Fatal(err)
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range detail.Labs {
		if row.Lab != nil && row.Lab.ID == lab.ID {
			count++
			if len(row.Questions) != 3 || row.Lab.Resources.AllocatedRequests.MemoryBytes != "0" {
				t.Fatalf("canonical row: %+v", row)
			}
		}
	}
	if count != 1 || detail.Group.Name == "" {
		t.Fatalf("count=%d group=%+v", count, detail.Group)
	}
}

func TestLabLifecycleForeignReadDenied(t *testing.T) {
	f, set, user, _ := prepareLifecycle(t)
	foreign, err := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.redID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.GetOwnLabLifecycle(context.Background(), f.eventID, user, foreign.ID); err == nil {
		t.Fatal("foreign Lab exposed")
	}
	if _, err = f.uc.GetOwnLabLifecycle(context.Background(), f.eventID, uuid.Must(uuid.NewV7()), foreign.ID); err == nil {
		t.Fatal("nonmember exposed")
	}
}

func TestInitialParticipantRuntimeUsesCurrentProducerReadiness(t *testing.T) {
	f, set, user, _ := prepareLifecycle(t)
	board, err := f.uc.ListOwnBoard(context.Background(), f.eventID, user)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, question := range board.Challenges {
		if question.EventChallengeID == set.challenges[0] {
			found = true
			if question.Lab == nil || question.Lab.RuntimeState != "ready" {
				t.Fatalf("current initial readiness missing: %+v", question.Lab)
			}
		}
	}
	if !found {
		t.Fatal("question missing")
	}
}

func TestRuntimeReadDoesNotPromoteHistoricalReadyDuringRestart(t *testing.T) {
	f, set, user, _ := prepareLifecycle(t)
	ctx := context.Background()
	// A new Running intent has cleared readiness, while the old binding and
	// agent Ready remain visible until restore/current observation converges.
	if _, err := f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET desired_revision=desired_revision+1,operation_id=$3,runtime_ready=false,actual_state='Starting' WHERE event_team_id=$1 AND id=(SELECT lab_id FROM lab_bindings WHERE event_team_id=$1 AND event_challenge_id=$2)`, f.blueID, set.challenges[0], uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	got, err := f.uc.GetOwnChallengeRuntime(ctx, f.eventID, user, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Lab == nil || got.Lab.RuntimeState != "preparing" || got.Status.Ready || len(got.Status.Access) != 0 {
		t.Fatalf("historical Ready promoted restart %+v", got)
	}
}
