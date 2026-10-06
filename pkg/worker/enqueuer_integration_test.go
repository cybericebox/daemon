package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/pkg/worker"
)

// blockingWorker holds every check in the running state until released.
type blockingWorker struct {
	river.WorkerDefaults[jobsModel.TestDeployRemovalCheckArgs]
	started chan int
	release chan struct{}
}

func (w *blockingWorker) Work(ctx context.Context, job *river.Job[jobsModel.TestDeployRemovalCheckArgs]) error {
	w.started <- job.Args.Attempt
	select {
	case <-w.release:
	case <-ctx.Done():
	}
	return nil
}

type registry struct {
	w river.Worker[jobsModel.TestDeployRemovalCheckArgs]
}

func (r registry) RegisterAll(workers *river.Workers) { river.AddWorker(workers, r.w) }
func (registry) PeriodicJobs() []*river.PeriodicJob   { return nil }

// TestEnqueueUniqueAt_AgainstRiver pins the unique options against real River (it rejects a ByState without the
// running state) and the chain behaviour: the running check queues its successor, the same attempt is deduplicated.
func TestEnqueueUniqueAt_AgainstRiver(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &blockingWorker{started: make(chan int, 4), release: make(chan struct{})}
	wc := worker.NewWorkerClient(db.Pool, worker.Retention{})
	wc.Initialize(ctx, registry{w: w})
	defer wc.Stop(context.Background())
	enq := wc.NewEnqueuer()

	args := func(attempt int) jobsModel.TestDeployRemovalCheckArgs {
		return jobsModel.TestDeployRemovalCheckArgs{DeployID: uuid.Must(uuid.NewV7()), Attempt: attempt}
	}
	first := args(0)
	if err := enq.EnqueueUniqueAt(ctx, first, time.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-w.started:
	case <-time.After(15 * time.Second):
		t.Fatal("the check never started")
	}

	// The check is running: it must be able to queue the next attempt.
	next := first
	next.Attempt = 1
	if err := enq.EnqueueUniqueAt(ctx, next, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("successor of a running check: %v", err)
	}
	// The same attempt twice is one job.
	if err := enq.EnqueueUniqueAt(ctx, next, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'test_deploy_removal_check'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("jobs = %d, want 2 (running attempt 0 and one queued attempt 1)", count)
	}
	close(w.release)
}
