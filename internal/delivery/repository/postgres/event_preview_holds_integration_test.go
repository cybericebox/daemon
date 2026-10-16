package postgres_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/cybericebox/daemon/internal/testhelpers"
	calendar "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

type previewCalendarTx struct {
	uow postgres.IUnitOfWorker[resourceCalendarRepo.Queries]
}

func (t previewCalendarTx) Do(ctx context.Context, fn func(context.Context, calendar.Store) error) error {
	txCtx, q, unit, err := t.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if err = fn(txCtx, resourceCalendarRepo.New(q)); err != nil {
		return err
	}
	return unit.Save()
}

type previewCalendarEvent struct{ id uuid.UUID }

func (e previewCalendarEvent) GetByID(context.Context, uuid.UUID) (eventModel.Event, error) {
	return eventModel.Event{ID: e.id, InfrastructureAllowed: true}, nil
}

type previewCalendarUsage struct{ id uuid.UUID }

func (e previewCalendarUsage) Usage(context.Context, time.Time) (calendar.Usage, error) {
	return calendar.Usage{ByEvent: map[uuid.UUID]calendar.Amount{e.id: {CPUMillicores: 600, MemoryBytes: 600}}}, nil
}
func TestEventPreviewCalendarAtomicBudgetAndCleanupPersistence(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	e := mustSeedEventForParticipants(t, db, "previewbudget")
	owner := mustSeedUser(t, db, "event-preview-budget@test.test")
	now := time.Now().UTC()
	repo := resourceCalendarRepo.New(db.Queries)
	r, err := calModel.NewEventReservation(calModel.EventInput{EventID: e.ID, Window: calModel.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}, Teams: 1, PerTeam: calModel.Amount{CPUMillicores: 1000, MemoryBytes: 1000}}, e.CreatedBy.UUID, now)
	require.NoError(t, err)
	r.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 1}}, 0, now)
	require.NoError(t, repo.CreateReservation(ctx, r))
	u := calendar.New(calendar.Dependencies{Store: repo, Tx: previewCalendarTx{postgres.NewUnitOfWorker[resourceCalendarRepo.Queries](postgres.NewUoWFactory(db.Pool))}, Events: previewCalendarEvent{e.ID}, Usage: previewCalendarUsage{e.ID}, Overhead: func() calendar.Amount { return calendar.Amount{CPUMillicores: 100, MemoryBytes: 100} }, Now: func() time.Time { return now }})
	var wg sync.WaitGroup
	out := make(chan struct {
		id  uuid.UUID
		err error
	}, 2)
	for range 2 {
		id := uuid.Must(uuid.NewV7())
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := u.AdmitEventTestLab(ctx, calendar.EventTestLabRequest{EventID: e.ID, Group: "tp-one-user", TestLabRequest: calendar.TestLabRequest{ID: id, Owner: owner, Size: calendar.Amount{CPUMillicores: 200, MemoryBytes: 200}, Lease: time.Hour}})
			out <- struct {
				id  uuid.UUID
				err error
			}{id, err}
		}()
	}
	wg.Wait()
	close(out)
	var admitted uuid.UUID
	success := 0
	for got := range out {
		if got.err == nil {
			success++
			admitted = got.id
		}
	}
	require.Equal(t, 1, success, "concurrent requests share one calendar lock and cannot both spend 300 remaining units")
	holds, err := repo.EventPreviewHolds(ctx, e.ID)
	require.NoError(t, err)
	require.Len(t, holds, 2)
	for _, h := range holds {
		require.Equal(t, e.ID, *h.EventID)
		require.Equal(t, "tp-one-user", h.GroupName)
	}
	_, err = repo.PurgeHolds(ctx, now.Add(2*time.Hour))
	require.NoError(t, err)
	holds, err = repo.EventPreviewHolds(ctx, e.ID)
	require.NoError(t, err)
	require.Len(t, holds, 2, "expired native allocations stay charged until cleanup")
	require.NoError(t, u.ReleaseEventTestLab(ctx, e.ID, admitted))
	holds, err = repo.EventPreviewHolds(ctx, e.ID)
	require.NoError(t, err)
	require.Empty(t, holds)
}
