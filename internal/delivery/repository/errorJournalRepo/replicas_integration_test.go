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

// twoReplicas builds two journals over one database, each with a repository of its own, as two daemon processes
// would have.
func twoReplicas(
	t *testing.T,
) (*errorJournalUseCase.Journal, *errorJournalUseCase.Journal, *testhelpers.TestDB) {
	t.Helper()
	repoA, db, enq := notificationRepo(t)
	repoB := errorJournalRepo.New(db.Queries, db.Pool, enq)
	cfg := errorJournalUseCase.DefaultConfig()
	cfg.StreamPollEvery = 20 * time.Millisecond
	a := errorJournalUseCase.New(errorJournalUseCase.Dependencies{Repo: repoA, Config: cfg})
	b := errorJournalUseCase.New(errorJournalUseCase.Dependencies{Repo: repoB, Config: cfg})
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

func TestARecordedErrorReachesAStreamOnAnotherReplicaThroughThePoller(t *testing.T) {
	a, b, _ := twoReplicas(t)
	ctx := context.Background()
	events, end := b.ErrorJournalStream()
	defer end()
	time.Sleep(200 * time.Millisecond) // the poller of b has its cursor

	rec, err := a.Record(
		ctx,
		errorJournal.Event{Kind: errorJournal.KindPanic, Source: "svc", Message: "boom"},
	)
	require.NoError(t, err)
	select {
	case e := <-events:
		require.Equal(t, rec.Group.ID, e.Group.ID)
		require.True(t, e.New)
		require.NotNil(t, e.Sample)
		require.Equal(t, "boom", e.Sample.Message)
	case <-time.After(10 * time.Second):
		t.Fatal("the stream of the other replica never got the recorded error")
	}

	// A status change on b reaches the stream of a.
	onA, endA := a.ErrorJournalStream()
	defer endA()
	time.Sleep(200 * time.Millisecond)
	_, err = b.SetErrorGroupStatus(ctx, rec.Group.ID, errorJournal.StatusResolved)
	require.NoError(t, err)
	select {
	case e := <-onA:
		require.Equal(t, errorJournal.StatusResolved, e.Group.Status)
	case <-time.After(10 * time.Second):
		t.Fatal("the status change never reached the other replica")
	}
}

func TestTheChangeLogIsInCommitOrderAndPurged(t *testing.T) {
	a, _, db := twoReplicas(t)
	ctx := context.Background()
	for i := range 5 {
		_, err := a.Record(
			ctx,
			errorJournal.Event{
				Kind:    errorJournal.KindPanic,
				Source:  "svc",
				Message: "boom",
				Count:   i + 1,
			},
		)
		require.NoError(t, err)
	}
	repo := errorJournalRepo.New(db.Queries, db.Pool)
	changes, err := repo.ChangesAfter(ctx, 0, 100)
	require.NoError(t, err)
	require.Len(t, changes, 5)
	for i := 1; i < len(changes); i++ {
		require.Greater(t, changes[i].Seq, changes[i-1].Seq)
	}
	_, err = db.Pool.Exec(
		ctx,
		"UPDATE error_journal_changes SET at = now() - interval '2 days' WHERE seq <= $1",
		changes[2].Seq,
	)
	require.NoError(t, err)
	_, err = repo.Purge(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	left, err := repo.ChangesAfter(ctx, 0, 100)
	require.NoError(t, err)
	require.Len(t, left, 2)
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
