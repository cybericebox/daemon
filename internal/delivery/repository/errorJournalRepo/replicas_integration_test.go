package errorJournalRepo_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/errorJournalRepo"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/testhelpers"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

// twoReplicas builds two journals over one database, each with a repository, a relay and a connection pool use of
// its own, as two daemon processes would have.
func twoReplicas(
	t *testing.T,
) (*errorJournalUseCase.Journal, *errorJournalUseCase.Journal, *testhelpers.TestDB) {
	t.Helper()
	repoA, db, enq := notificationRepo(t)
	repoB := errorJournalRepo.New(db.Queries, db.Pool, enq)
	cfg := errorJournalUseCase.DefaultConfig()
	a := errorJournalUseCase.New(
		errorJournalUseCase.Dependencies{
			Repo:   repoA,
			Relay:  errorJournalRepo.NewRelay(db.Pool),
			Config: cfg,
		},
	)
	b := errorJournalUseCase.New(
		errorJournalUseCase.Dependencies{
			Repo:   repoB,
			Relay:  errorJournalRepo.NewRelay(db.Pool),
			Config: cfg,
		},
	)
	return a, b, db
}

func countDeliveries(t *testing.T, db *testhelpers.TestDB) int {
	t.Helper()
	var n int
	require.NoError(
		t,
		db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM error_notification_deliveries").
			Scan(&n),
	)
	return n
}

func TestARecordedErrorReachesAStreamOnAnotherReplicaThroughPostgres(t *testing.T) {
	a, b, _ := twoReplicas(t)
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }() // the listeners end before the pool is closed
	for _, j := range []*errorJournalUseCase.Journal{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); j.Listen(ctx) }()
	}
	events, end := b.ErrorJournalStream()
	defer end()

	// LISTEN is set up asynchronously: record until the other replica hears it.
	deadline := time.After(10 * time.Second)
	for i := 0; ; i++ {
		rec, err := a.Record(
			ctx,
			errorJournal.Event{Kind: errorJournal.KindPanic, Source: "svc", Message: "boom"},
		)
		require.NoError(t, err)
		select {
		case e := <-events:
			require.Equal(t, rec.Group.ID, e.Group.ID)
			require.NotNil(t, e.Sample)
			require.Equal(t, "boom", e.Sample.Message)
			return
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatal("the stream of the other replica never got the recorded error")
		}
	}
}

func TestTwoReplicasEnqueueOneNotificationForOneError(t *testing.T) {
	a, b, db := twoReplicas(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, j := range []*errorJournalUseCase.Journal{a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := j.Record(
				ctx,
				errorJournal.Event{Kind: errorJournal.KindPanic, Source: "svc", Message: "boom"},
			)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(
		t,
		1,
		countDeliveries(t, db),
		"one message intent for the group, whichever replica recorded it",
	)
}

func TestTwoReplicasEnqueueOneNotificationPerCooldownForANoisyKind(t *testing.T) {
	a, b, db := twoReplicas(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i, j := range []*errorJournalUseCase.Journal{a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Different routes are different groups of the same noisy kind.
			_, err := j.Record(ctx, errorJournal.Event{
				Kind:       errorJournal.KindHTTP403,
				Source:     string(rune('a' + i)),
				Route:      "/api/" + string(rune('a'+i)),
				Method:     "GET",
				HTTPStatus: 403,
				Message:    "forbidden",
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(
		t,
		1,
		countDeliveries(t, db),
		"one message per cooldown for the kind across replicas",
	)
}
