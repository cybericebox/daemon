package event_test

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

type hangingDeviceAgent struct {
	*standAgent
	entered chan context.Context
}

func (a *hangingDeviceAgent) ResetDevice(ctx context.Context, _, _, _ string) error {
	a.entered <- ctx
	<-ctx.Done()
	return ctx.Err()
}
func (a *hangingDeviceAgent) RescueDevice(ctx context.Context, _, _, _ string, _ bool) error {
	a.entered <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func TestStandDeviceProductionDeadlineReleasesHangingRPCSolveAndSourceLocks(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	for i, ch := range set.challenges[:2] {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	next := publishLifecycleSource(t, f, set)
	before, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	var flag string
	if err = f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[2]).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	agent := &hangingDeviceAgent{f.agent, make(chan context.Context, 1)}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: agent, InfrastructureCapability: standCapability{}})
	// Caller has cancellation for cleanup, deliberately no deadline. The only
	// mutation deadline must come from the production entry point.
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, hasDeadline := caller.Deadline(); hasDeadline {
		t.Fatal("test supplied a deadline")
	}
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- uc.ResetStandDevice(caller, f.eventID, f.blueID, set.challenges[0], "web") }()
	watchdog, stopWatchdog := context.WithTimeout(ctx, 40*time.Second)
	defer stopWatchdog()
	var operation context.Context
	select {
	case operation = <-agent.entered:
	case <-watchdog.Done():
		t.Fatal(watchdog.Err())
	}
	deadline, bounded := operation.Deadline()
	if !bounded || deadline.Sub(started) > 31*time.Second || deadline.Sub(started) < 25*time.Second {
		t.Fatalf("production device operation is unbounded or has unexpected deadline: bounded=%t delta=%v", bounded, deadline.Sub(started))
	}
	solveDone := make(chan error, 1)
	go func() {
		_, err := f.uc.SubmitChallenge(caller, f.eventID, user, set.challenges[2], event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(2 * time.Second)})
		solveDone <- err
	}()
	waitDeviceLock(t, f, watchdog, "LockEventTeamForLabAdmission")
	sourceDone := make(chan error, 1)
	go func() {
		_, err := f.uc.ReplaceEventExercise(caller, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next, RecreateStands: true}, f.ownerID)
		sourceDone <- err
	}()
	waitDeviceLock(t, f, watchdog, "LockEventTeamsForLabSourceChange")
	select {
	case err = <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("hanging RPC returned %v", err)
		}
	case <-watchdog.Done():
		t.Fatal("production bound did not release RPC", watchdog.Err())
	}
	for name, ch := range map[string]<-chan error{"solve": solveDone, "source": sourceDone} {
		select {
		case err = <-ch:
			if err != nil {
				t.Fatalf("%s failed after timeout: %v", name, err)
			}
		case <-watchdog.Done():
			t.Fatalf("%s remains blocked after RPC timeout", name)
		}
	}
	requireTerminalSourcePreserved(t, f, set, beforeWithSolved(t, f, before.ID), 3)
	// Timeout rolled the guard transaction back and returned its connection.
	var open int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction' AND datname=current_database()`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("open transactions %d %v", open, err)
	}
}

func beforeWithSolved(t *testing.T, f *standFixture, id uuid.UUID) (lab eventLabModel.Lab) {
	t.Helper()
	lab, err := eventLabRepo.New(f.db.Queries).Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if lab.CloseReason != "solved" {
		t.Fatal("final solve did not close Lab", lab)
	}
	return lab
}

func waitDeviceLock(t *testing.T, f *standFixture, ctx context.Context, query string) {
	t.Helper()
	for {
		var waiting bool
		if err := f.db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%'||$1||'%')`, query).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("query never waited on lock: %s", query)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestStandDeviceHonorsShorterCallerDeadlineAndCancellationWhileLocking(t *testing.T) {
	for _, mode := range []string{"shorter deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f, set, _, _ := prepareLifecycle(t)
			ctx := context.Background()
			holder, err := f.db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback(ctx)
			if _, err = holder.Exec(ctx, `SELECT id FROM event_teams WHERE id=$1 FOR UPDATE`, f.blueID); err != nil {
				t.Fatal(err)
			}
			agent := &hangingDeviceAgent{f.agent, make(chan context.Context, 1)}
			uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: agent, InfrastructureCapability: standCapability{}})
			var caller context.Context
			var cancel context.CancelFunc
			if mode == "shorter deadline" {
				caller, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
			} else {
				caller, cancel = context.WithCancel(ctx)
			}
			defer cancel()
			done := make(chan error, 1)
			started := time.Now()
			go func() { done <- uc.RescueStandDevice(caller, f.eventID, f.blueID, set.challenges[0], "web", true) }()
			if mode == "cancel" {
				watchdog, stop := context.WithTimeout(ctx, 2*time.Second)
				defer stop()
				waitDeviceLock(t, f, watchdog, "LockEventTeamForLabAdmission")
				cancel()
			}
			select {
			case err = <-done:
				if err == nil || time.Since(started) > 2*time.Second {
					t.Fatalf("caller limit ignored: %v after %v", err, time.Since(started))
				}
			case <-time.After(2 * time.Second):
				t.Fatal("caller limit did not release mutation")
			}
			if len(agent.entered) != 0 {
				t.Fatal("RPC invoked while admission was locked")
			}
		})
	}
}
