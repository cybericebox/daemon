package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var rcT0 = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)

func rcWindow(from, to int) calModel.Window {
	w, _ := calModel.NewWindow(rcT0.Add(time.Duration(from)*time.Hour), rcT0.Add(time.Duration(to)*time.Hour))
	return w
}

// A reservation round-trips with its placement, one event has one active reservation, the window query finds
// the overlaps, and a canceled reservation frees the event for a new one.
func TestResourceCalendarReservations(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := resourceCalendarRepo.New(db.Queries)
	e := mustSeedEventForParticipants(t, db, "rcres")

	r, err := calModel.NewEventReservation(calModel.EventInput{
		EventID: e.ID, Window: rcWindow(0, 4), Teams: 3, PerTeam: calModel.Amount{CPUMillicores: 500, MemoryBytes: 1 << 29},
		LargestDevice: calModel.Amount{CPUMillicores: 250, MemoryBytes: 1 << 30}, BufferPercent: 15, TailGap: time.Hour,
	}, e.CreatedBy.UUID, rcT0)
	require.NoError(t, err)
	agent := uuid.Must(uuid.NewV7())
	r.SetPlacement([]calModel.Share{{AgentID: agent, Units: 2}}, 1, rcT0)
	require.NoError(t, repo.CreateReservation(ctx, r))

	got, err := repo.GetEventReservation(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, r.Size, got.Size)
	assert.Equal(t, r.Window, got.Window)
	assert.Equal(t, []calModel.Share{{AgentID: agent, Units: 2}}, got.Placement)
	assert.Equal(t, 1, got.Unplaced)
	assert.Equal(t, time.Hour, got.TailGap)

	dup, _ := calModel.NewEventReservation(calModel.EventInput{EventID: e.ID, Window: rcWindow(0, 2), Teams: 1, PerTeam: calModel.Amount{CPUMillicores: 1}}, e.CreatedBy.UUID, rcT0)
	assert.Error(t, repo.CreateReservation(ctx, dup), "one active reservation per event")

	in, err := repo.ListInWindow(ctx, rcWindow(3, 5))
	require.NoError(t, err)
	assert.Len(t, in, 1)
	out, err := repo.ListInWindow(ctx, rcWindow(4, 6))
	require.NoError(t, err)
	assert.Empty(t, out, "the window is half-open")

	labels, err := repo.Labels(ctx, []uuid.UUID{r.ID})
	require.NoError(t, err)
	assert.Equal(t, e.Name, labels[r.ID].EventName)

	got.Cancel(rcT0.Add(time.Minute))
	ok, err := repo.UpdateReservation(ctx, got)
	require.NoError(t, err)
	assert.True(t, ok)
	_, err = repo.GetEventReservation(ctx, e.ID)
	assert.True(t, repositoryTools.IsObjectNotFoundError(err))
	require.NoError(t, repo.CreateReservation(ctx, dup), "a canceled reservation frees the event")
}

// Change requests are decided once; the list is filtered by status and event with the event name.
func TestResourceCalendarChangeRequests(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := resourceCalendarRepo.New(db.Queries)
	e := mustSeedEventForParticipants(t, db, "rcchange")
	r, _ := calModel.NewEventReservation(calModel.EventInput{EventID: e.ID, Window: rcWindow(0, 4), Teams: 2, PerTeam: calModel.Amount{CPUMillicores: 500, MemoryBytes: 1 << 29}}, e.CreatedBy.UUID, rcT0)
	require.NoError(t, repo.CreateReservation(ctx, r))

	size := calModel.Amount{CPUMillicores: 4000, MemoryBytes: 4 << 30}
	end := rcT0.Add(8 * time.Hour)
	c, err := calModel.NewChangeRequest(r, e.CreatedBy.UUID, &size, nil, nil, &end, "more teams joined", rcT0)
	require.NoError(t, err)
	require.NoError(t, repo.CreateChangeRequest(ctx, c))

	pending := calModel.ChangePending
	list, err := repo.ListChangeRequests(ctx, &pending, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, e.Name, list[0].EventName)
	assert.Equal(t, size, *list[0].Size)
	assert.Nil(t, list[0].Dynamic)
	assert.True(t, list[0].WindowEnd.Equal(end))
	n, _ := repo.CountPendingChangeRequests(ctx)
	assert.EqualValues(t, 1, n)

	got, err := repo.GetChangeRequest(ctx, c.ID)
	require.NoError(t, err)
	require.NoError(t, got.Decide(true, e.CreatedBy.UUID, "ok", rcT0.Add(time.Hour)))
	ok, err := repo.DecideChangeRequest(ctx, got)
	require.NoError(t, err)
	assert.True(t, ok)
	again, _ := repo.GetChangeRequest(ctx, c.ID)
	again.Status = calModel.ChangeRejected
	ok, err = repo.DecideChangeRequest(ctx, again)
	require.NoError(t, err)
	assert.False(t, ok, "decided meanwhile")

	list, _ = repo.ListChangeRequests(ctx, &pending, nil)
	assert.Empty(t, list)
	other := uuid.Must(uuid.NewV7())
	list, _ = repo.ListChangeRequests(ctx, nil, &other)
	assert.Empty(t, list)
}

// One open alarm per cause; resolving it lets the cause raise a new one.
func TestResourceCalendarAlarmsSettingsAndHolds(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := resourceCalendarRepo.New(db.Queries)
	e := mustSeedEventForParticipants(t, db, "rcalarm")
	r, _ := calModel.NewEventReservation(calModel.EventInput{EventID: e.ID, Window: rcWindow(0, 4), Teams: 2, PerTeam: calModel.Amount{CPUMillicores: 500, MemoryBytes: 1 << 29}}, e.CreatedBy.UUID, rcT0)
	require.NoError(t, repo.CreateReservation(ctx, r))

	a, err := calModel.NewAlarm(calModel.AlarmNotPlaced, r, nil, 2, calModel.Amount{CPUMillicores: 1000}, rcT0)
	require.NoError(t, err)
	require.NoError(t, repo.CreateAlarm(ctx, a))
	dup, _ := calModel.NewAlarm(calModel.AlarmNotPlaced, r, nil, 1, calModel.Amount{}, rcT0)
	assert.Error(t, repo.CreateAlarm(ctx, dup), "one open alarm per cause")
	agent := uuid.Must(uuid.NewV7())
	lost, _ := calModel.NewAlarm(calModel.AlarmAgentLost, r, &agent, 1, calModel.Amount{}, rcT0)
	require.NoError(t, repo.CreateAlarm(ctx, lost), "another cause is another alarm")

	open, err := repo.GetOpenAlarm(ctx, calModel.AlarmNotPlaced, r.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, a.ID, open.ID)
	open.Acknowledge(e.CreatedBy.UUID, rcT0.Add(time.Minute))
	ok, err := repo.UpdateAlarm(ctx, open)
	require.NoError(t, err)
	assert.True(t, ok)
	open.Resolve(rcT0.Add(time.Hour))
	_, err = repo.UpdateAlarm(ctx, open)
	require.NoError(t, err)
	require.NoError(t, repo.CreateAlarm(ctx, dup), "a resolved cause may be raised again")

	list, err := repo.ListAlarms(ctx, true, 50)
	require.NoError(t, err)
	assert.Len(t, list, 2)
	assert.Equal(t, e.Name, list[0].EventName)
	all, _ := repo.ListAlarms(ctx, false, 50)
	assert.Len(t, all, 3)

	s, err := repo.Settings(ctx)
	require.NoError(t, err)
	assert.Equal(t, calModel.Amount{}, s.TestPool)
	require.NoError(t, repo.SetSettings(ctx, calModel.Settings{TestPool: calModel.Amount{CPUMillicores: 2000, MemoryBytes: 4 << 30}, UpdatedAt: rcT0}))
	s, _ = repo.Settings(ctx)
	assert.Equal(t, int64(2000), s.TestPool.CPUMillicores)

	owner := mustSeedUser(t, db, "rchold@test.test")
	hold := calModel.TestLabHold{ID: uuid.Must(uuid.NewV7()), OwnerID: owner, Via: calModel.ViaPool, Size: calModel.Amount{CPUMillicores: 500, MemoryBytes: 1 << 29}, StartsAt: rcT0, ExpiresAt: rcT0.Add(2 * time.Hour)}
	require.NoError(t, repo.SaveHold(ctx, hold))
	holds, err := repo.ActiveHolds(ctx, rcT0.Add(time.Hour))
	require.NoError(t, err)
	assert.Len(t, holds, 1)
	holds, _ = repo.ActiveHolds(ctx, rcT0.Add(3*time.Hour))
	assert.Empty(t, holds, "the lease is over")
	purged, _ := repo.PurgeHolds(ctx, rcT0.Add(3*time.Hour))
	assert.EqualValues(t, 1, purged)
	require.NoError(t, repo.SaveHold(ctx, hold))
	require.NoError(t, repo.DeleteHold(ctx, hold.ID))
}

func TestResourceCalendarStorageRoundTripAndConstraints(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := resourceCalendarRepo.New(db.Queries)
	e := mustSeedEventForParticipants(t, db, "storagequota")
	r, err := calModel.NewEventReservation(calModel.EventInput{EventID: e.ID, Window: rcWindow(0, 4), Teams: 2, PerTeam: calModel.Amount{CPUMillicores: 500, MemoryBytes: 1 << 29}, PerTeamSnapshotQuotaBytes: 1 << 30, DynamicSnapshotQuotaBytes: 3 << 30}, e.CreatedBy.UUID, rcT0)
	require.NoError(t, err)
	require.NoError(t, repo.CreateReservation(ctx, r))
	got, err := repo.GetEventReservation(ctx, e.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 5<<30, got.SizeSnapshotQuotaBytes)
	assert.EqualValues(t, 1<<30, got.PerTeamSnapshotQuotaBytes)
	assert.EqualValues(t, 3<<30, got.DynamicSnapshotQuotaBytes)
	_, err = db.Pool.Exec(ctx, `UPDATE resource_reservations SET per_team_snapshot_quota_bytes=-1 WHERE id=$1`, r.ID)
	require.Error(t, err)
	quota := int64(6 << 30)
	dynamic := int64(4 << 30)
	change, err := calModel.NewChangeRequest(r, e.CreatedBy.UUID, &r.Size, nil, nil, nil, "storage quota", rcT0)
	require.NoError(t, err)
	change.SizeSnapshotQuotaBytes = &quota
	change.DynamicSnapshotQuotaBytes = &dynamic
	require.NoError(t, repo.CreateChangeRequest(ctx, change))
	saved, err := repo.GetChangeRequest(ctx, change.ID)
	require.NoError(t, err)
	require.Equal(t, quota, *saved.SizeSnapshotQuotaBytes)
	require.Equal(t, dynamic, *saved.DynamicSnapshotQuotaBytes)
	list, err := repo.ListChangeRequests(ctx, nil, &e.ID)
	require.NoError(t, err)
	require.Equal(t, quota, *list[0].SizeSnapshotQuotaBytes)
	settings, err := repo.Settings(ctx)
	require.NoError(t, err)
	settings.TestPoolSnapshotQuotaBytes = 7 << 30
	require.NoError(t, repo.SetSettings(ctx, settings))
	settings, err = repo.Settings(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 7<<30, settings.TestPoolSnapshotQuotaBytes)
	hold := calModel.TestLabHold{ID: uuid.Must(uuid.NewV7()), OwnerID: e.CreatedBy.UUID, Via: calModel.ViaPool, Size: calModel.Amount{CPUMillicores: 20, MemoryBytes: 32 << 20}, SnapshotQuotaBytes: 1 << 30, StartsAt: rcT0, ExpiresAt: rcT0.Add(time.Hour)}
	require.NoError(t, repo.SaveHold(ctx, hold))
	hold.SnapshotQuotaBytes = 0
	hold.ExpiresAt = rcT0.Add(2 * time.Hour)
	require.NoError(t, repo.SaveHold(ctx, hold))
	holds, err := repo.ActiveHolds(ctx, rcT0)
	require.NoError(t, err)
	require.EqualValues(t, 1<<30, holds[0].SnapshotQuotaBytes, "lease renewal cannot silently erase storage quota")
}
