package errorJournalRepo_test

import (
	"context"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestSMTPProviderRecord_DeduplicatesConcurrentCapturesAndLateRecoveryEvents(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	fp := "provider-regression"
	at := t0.Add(123456789 * time.Nanosecond)
	record := func(at time.Time) (errorJournalUseCase.RecordResult, error) {
		return r.Record(ctx, errorJournalUseCase.RecordInput{Fingerprint: fp, Kind: errorJournal.KindMail, Source: "smtp-provider:" + id.String(), Title: "SMTP failure", At: at, Keep: 5, Count: 1, Sample: errorJournal.Sample{ID: uuid.Must(uuid.NewV7()), OccurredAt: at, Message: "timeout"}})
	}
	var wg sync.WaitGroup
	results := make(chan errorJournalUseCase.RecordResult, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); res, err := record(at); results <- res; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var groupID uuid.UUID
	skipped := 0
	for res := range results {
		groupID = res.Group.ID
		if res.Skipped {
			skipped++
		}
	}
	require.Equal(t, 7, skipped)
	detail, err := r.GetGroup(ctx, groupID)
	require.NoError(t, err)
	require.Equal(t, int64(1), detail.Occurrences)
	success := at.Add(time.Minute)
	require.NoError(t, r.ResolveByFingerprint(ctx, fp, success))
	late, err := record(at)
	require.NoError(t, err)
	require.True(t, late.Skipped)
	require.Equal(t, errorJournal.StatusResolved, late.Group.Status)
	older, err := record(at.Add(30 * time.Second))
	require.NoError(t, err)
	require.True(t, older.Skipped, "a late capture before success cannot reopen")
	newFailure, err := record(success.Add(time.Second))
	require.NoError(t, err)
	require.True(t, newFailure.Reopened)
	require.Equal(t, int64(2), newFailure.Group.Occurrences)
	require.NoError(t, r.ResolveByFingerprint(ctx, fp, success))
	detail, err = r.GetGroup(ctx, groupID)
	require.NoError(t, err)
	require.Equal(t, errorJournal.StatusOpen, detail.Status, "old success cannot resolve a newer failure")
	samples, err := r.ListSamples(ctx, groupID, 10)
	require.NoError(t, err)
	require.Len(t, samples, 2)
}
