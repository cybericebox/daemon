package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

type pendingDeviceAgent struct {
	*standAgent
	entered, release chan struct{}
}

func (a *pendingDeviceAgent) ResetDevice(ctx context.Context, group, lab, device string) error {
	close(a.entered)
	select {
	case <-a.release:
		return a.standAgent.ResetDevice(ctx, group, lab, device)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestLogicalClosureSerializesInflightAndDeniesQueuedDeviceWrites(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, ch := range set.challenges[:2] {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	agent := &pendingDeviceAgent{f.agent, make(chan struct{}), make(chan struct{})}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: agent, InfrastructureCapability: standCapability{}})
	resetDone := make(chan error, 1)
	go func() { resetDone <- uc.ResetStandDevice(ctx, f.eventID, f.blueID, set.challenges[0], "web") }()
	select {
	case <-agent.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var flag string
	if err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[2]).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	solveDone := make(chan error, 1)
	go func() {
		_, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[2], event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(2 * time.Second)})
		solveDone <- err
	}()
	// Observe the answer waiting on the real admission row held across the
	// already-admitted legacy RPC. Closure must not commit while it is in flight.
	for {
		var waiting bool
		if err := f.db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockEventTeamForLabAdmission%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-solveDone:
			t.Fatalf("closure passed in-flight write: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	// The queued UI rescue is admitted behind that waiting final answer.
	rescueDone := make(chan error, 1)
	go func() { rescueDone <- uc.RescueStandDevice(ctx, f.eventID, f.blueID, set.challenges[1], "web", true) }()
	for {
		var waiters int
		if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockEventTeamForLabAdmission%'`).Scan(&waiters); err != nil {
			t.Fatal(err)
		}
		if waiters >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(agent.release)
	if err := <-resetDone; err != nil {
		t.Fatal(err)
	}
	if err := <-solveDone; err != nil {
		t.Fatal(err)
	}
	if err := <-rescueDone; err == nil {
		t.Fatal("queued post-closure rescue reached agent")
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.deviceCalls) != 1 {
		t.Fatalf("mutations=%v", agent.deviceCalls)
	}
}
