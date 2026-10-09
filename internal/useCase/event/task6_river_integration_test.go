package event_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	lablifecycleJob "github.com/cybericebox/daemon/internal/jobs/lablifecycle"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

// This executes a real River PostgreSQL queue and its periodic scheduler. The
// infrastructure fixture is still the existing recording agent: it is queue /
// transaction evidence, never native/network/browser end-to-end evidence.
func task6RiverClient(t *testing.T, f *standFixture, uc *event.EventUseCase) *river.Client[pgx.Tx] {
	t.Helper()
	workers := river.NewWorkers()
	river.AddWorker(workers, lablifecycleJob.NewWorker(uc))
	client, err := river.NewClient(riverpgxv5.New(f.db.Pool), &river.Config{
		Workers:           workers,
		Queues:            map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
		FetchCooldown:     10 * time.Millisecond,
		FetchPollInterval: 50 * time.Millisecond,
		PeriodicJobs: []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(100*time.Millisecond), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.LabLifecycleArgs{}, &river.InsertOpts{MaxAttempts: 1}
		}, &river.PeriodicJobOpts{RunOnStart: true})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func task6StartRiver(t *testing.T, ctx context.Context, client *river.Client[pgx.Tx]) {
	t.Helper()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Stop(stop); err != nil {
			t.Error(err)
		}
	})
}
func task6WaitLab(t *testing.T, ctx context.Context, f *standFixture, id uuid.UUID, predicate func(eventLabModel.Lab) bool) eventLabModel.Lab {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		value, err := eventLabRepo.New(f.db.Queries).Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(value) {
			return value
		}
		select {
		case <-ctx.Done():
			t.Fatal("real River lifecycle did not converge", ctx.Err())
		case <-ticker.C:
		}
	}
}
func TestTask6RealRiverPeriodicLostWakeAndWorkerRestart(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	unrelated := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrator, err := rivermigrate.New(riverpgxv5.New(f.db.Pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	// Simulate a lost application wake before the worker process exists.
	f.uc.SetLabLifecycleWake(func(context.Context) error { return fmt.Errorf("wake unavailable") })
	for i, question := range set.challenges {
		result := submitStoredFlag(t, f, user, question, at.Add(time.Duration(i)*time.Second))
		if result.Lab.LogicalClosed != (i == 2) {
			t.Fatal("wrong closure point")
		}
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, unrelated.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	other, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.redID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	fresh := func() *event.EventUseCase {
		return event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: f.agent, InfrastructureCapability: standCapability{}})
	}
	first := task6RiverClient(t, f, fresh())
	task6StartRiver(t, ctx, first)
	var completed int
	ticker := time.NewTicker(20 * time.Millisecond)
	for completed == 0 {
		if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind=$1 AND state='completed'`, jobsModel.LabLifecycleArgs{}.Kind()).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	ticker.Stop()
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	if err != nil || current.Allocation.RuntimeState == "Released" {
		t.Fatal("acceptance credited native release", err)
	}
	if err = first.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	if len(f.agent.stopCalls) == 0 {
		f.agent.mu.Unlock()
		t.Fatal("River periodic scheduler never dispatched stop")
	}
	observed := time.Now()
	f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{lab.Ref: {Ref: lab.Ref, UID: lab.AgentUID, Generation: 2, ObservedGeneration: 2, OperationID: lab.OperationID, Revision: lab.Revision, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "NotRequired", AccessFenced: true, AccessFencedAt: &observed, AccessFenceVPNBootID: "fixture-boot", StoppedAt: &observed, ObservedAt: &observed, Allocation: eventLabModel.Allocation{RuntimeState: "Released", StorageState: "None", ReleasedAt: &observed}}}
	f.agent.mu.Unlock()
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET next_attempt_at=$1 WHERE id=$2`, time.Now(), lab.ID); err != nil {
		t.Fatal(err)
	}
	// A new River client and new use case recover solely from persisted intent.
	second := task6RiverClient(t, f, fresh())
	task6StartRiver(t, ctx, second)
	final := task6WaitLab(t, ctx, f, lab.ID, func(value eventLabModel.Lab) bool {
		return value.ObservedRevision == value.Revision && value.Allocation.RuntimeState == "Released"
	})
	if final.CloseReason != "solved" || final.Ref != lab.Ref {
		t.Fatal("terminal identity changed")
	}
	for _, before := range []eventLabModel.Lab{sibling, other} {
		after, err := eventLabRepo.New(f.db.Queries).Get(ctx, before.ID)
		if err != nil || after.Revision != before.Revision || after.ClosedAt != nil {
			t.Fatal("sibling/team changed", err)
		}
	}
	jobs, err := second.JobList(ctx, river.NewJobListParams().Kinds(jobsModel.LabLifecycleArgs{}.Kind()).States(rivertype.JobStateCompleted))
	if err != nil || len(jobs.Jobs) == 0 {
		t.Fatal("real queue completion missing", err)
	}
}
