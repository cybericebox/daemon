package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	notifyJob "github.com/cybericebox/daemon/internal/jobs/notify"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
)

type durableNotifyProcessor struct{ handled chan uuid.UUID }

func (p *durableNotifyProcessor) ProcessJournalDelivery(_ context.Context, id uuid.UUID) error {
	p.handled <- id
	return nil
}
func (p *durableNotifyProcessor) ProcessAuthDelivery(_ context.Context, id uuid.UUID) error {
	p.handled <- id
	return nil
}
func (*durableNotifyProcessor) ProcessNotification(context.Context, dispatchModel.ProcessInput) error {
	return errors.New("tagged command reached ordinary processor")
}
func (*durableNotifyProcessor) LoadNotificationPayload(context.Context, uuid.UUID) (dispatchModel.Payload, error) {
	return dispatchModel.Payload{}, errors.New("tagged command read ordinary payload")
}
func (*durableNotifyProcessor) DiscardNotificationPayload(context.Context, uuid.UUID) error {
	return errors.New("tagged command discarded ordinary payload")
}

type durableNotifyRegistry struct{ processor *durableNotifyProcessor }

func (r durableNotifyRegistry) RegisterAll(workers *river.Workers) {
	river.AddWorker(workers, notifyJob.NewWorker(r.processor))
}
func (durableNotifyRegistry) PeriodicJobs() []*river.PeriodicJob { return nil }

func TestTaggedNotifyCommandsUseAndRunOnVersionedQueue(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &durableNotifyProcessor{handled: make(chan uuid.UUID, 2)}
	wc := worker.NewWorkerClient(db.Pool, worker.Retention{})
	wc.Initialize(ctx, durableNotifyRegistry{p})
	defer wc.Stop(context.Background())
	journalID, authID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, args := range []jobsModel.NotifyArgs{{JournalDeliveryID: &journalID}, {AuthDeliveryID: &authID}} {
		require.NoError(t, wc.NewEnqueuer().Enqueue(ctx, args))
	}
	handled := []uuid.UUID{}
	for range 2 {
		select {
		case id := <-p.handled:
			handled = append(handled, id)
		case <-time.After(15 * time.Second):
			t.Fatal("versioned notify queue was not consumed")
		}
	}
	require.ElementsMatch(t, []uuid.UUID{journalID, authID}, handled)
	var n int
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='notify' AND queue=$1", jobsModel.NotifyDurableQueue).Scan(&n))
	require.Equal(t, 2, n)
}
