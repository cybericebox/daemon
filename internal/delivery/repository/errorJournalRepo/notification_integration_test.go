package errorJournalRepo_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/errorJournalRepo"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/testhelpers"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
)

type transactionEnqueuer struct {
	worker.IEnqueuer
	client *river.Client[pgx.Tx]
	fail   bool
}

func (e *transactionEnqueuer) EnqueueTx(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	if e.fail {
		return errors.New("queue insert failed")
	}
	_, err := e.client.InsertTx(ctx, tx, args, nil)
	return err
}

func notificationRepo(t *testing.T) (*errorJournalRepo.Repository, *testhelpers.TestDB, *transactionEnqueuer) {
	t.Helper()
	_, db := newRepo(t)
	ctx := context.Background()
	migrator, err := rivermigrate.New(riverpgxv5.New(db.Pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	client, err := river.NewClient(riverpgxv5.New(db.Pool), &river.Config{})
	require.NoError(t, err)
	enq := &transactionEnqueuer{IEnqueuer: worker.NewEnqueuer(), client: client}
	return errorJournalRepo.New(db.Queries, db.Pool, enq), db, enq
}
func plannedRecord(r *errorJournalRepo.Repository, fp string) (errorJournalUseCase.RecordResult, error) {
	return r.Record(context.Background(), errorJournalUseCase.RecordInput{
		Fingerprint: fp, Kind: errorJournal.KindPanic, Title: "panic", At: t0, Keep: 3, Count: 1,
		Sample: errorJournal.Sample{ID: uuid.Must(uuid.NewV7()), OccurredAt: t0, Message: "boom"},
		Notification: func(errorJournalUseCase.RecordResult) *errorJournalUseCase.NotificationPlan {
			return &errorJournalUseCase.NotificationPlan{Now: t0, Cutoff: t0.Add(-time.Minute), Body: "telegram body", EmailBody: "full email body"}
		},
	})
}
func TestNotificationCaptureAndRiverCommandCommitAtomically(t *testing.T) {
	r, db, enq := notificationRepo(t)
	ctx := context.Background()
	enq.fail = true
	_, err := plannedRecord(r, "rollback")
	require.Error(t, err)
	var n int
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM error_groups").Scan(&n))
	require.Zero(t, n)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM error_notification_deliveries").Scan(&n))
	require.Zero(t, n)
	enq.fail = false
	rec, err := plannedRecord(r, "commit")
	require.NoError(t, err)
	rows, err := r.ListDeliveries(ctx, rec.Group.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "prepare", rows[0].Channel)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind = 'notify' AND args->>'journal_delivery_id' = $1", rows[0].ID.String()).Scan(&n))
	require.Equal(t, 1, n)
	g, err := r.GetGroup(ctx, rec.Group.ID)
	require.NoError(t, err)
	require.Nil(t, g.LastNotifiedAt)
}
func TestNotificationConcurrentCaptureExpansionAndRestart(t *testing.T) {
	r, db, enq := notificationRepo(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := plannedRecord(r, "race"); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var id uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT id FROM error_groups WHERE fingerprint='race'").Scan(&id))
	roots, err := r.ListDeliveries(ctx, id)
	require.NoError(t, err)
	require.Len(t, roots, 1, "only one intent across replicas")
	restarted := errorJournalRepo.New(db.Queries, db.Pool, enq)
	targets := []errorJournalUseCase.DeliveryTarget{{Channel: "email", Target: "ops@example.org"}, {Channel: "telegram", Target: "100"}}
	errs = make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- restarted.ExpandDelivery(ctx, roots[0], targets, t0) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	children, err := restarted.ListDeliveries(ctx, id)
	require.NoError(t, err)
	require.Len(t, children, 2)
	var n int
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='notify'").Scan(&n))
	require.Equal(t, 3, n, "one prepare command and two targets")
	require.NoError(t, restarted.StartDelivery(ctx, children[0].ID, t0))
	require.NoError(t, restarted.FinishDelivery(ctx, children[0], errorJournalUseCase.TestResult{Error: "unreachable"}, t0, t0.Add(time.Minute)))
	durable, err := r.GetDelivery(ctx, children[0].ID)
	require.NoError(t, err)
	require.Equal(t, 1, durable.Attempts)
	require.Equal(t, "unreachable", durable.LastError)
	require.Nil(t, durable.DeliveredAt)
	require.NoError(t, restarted.FinishDelivery(ctx, durable, errorJournalUseCase.TestResult{OK: true}, t0.Add(time.Minute), t0.Add(time.Minute)))
	durable, err = r.GetDelivery(ctx, durable.ID)
	require.NoError(t, err)
	require.NotNil(t, durable.DeliveredAt)
}

func TestStableDispatchIDIsClaimedOnceAcrossReplicas(t *testing.T) {
	_, db := newRepo(t)
	r := dispatchRepo.New(db.Queries)
	id, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	claims := make(chan bool, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := r.CreateIfAbsent(context.Background(), id, "password_reset", userID, nil, nil)
			claims <- created
			errs <- err
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	n := 0
	for created := range claims {
		if created {
			n++
		}
	}
	require.Equal(t, 1, n)
}
