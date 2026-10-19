package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// A calendar part round-trips; a newer desired revision survives an older confirmation; the observed status is a
// narrow write; one live part per calendar reservation and agent; units keep their first agent.
func TestClusterReservationParts(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := resourceCalendarRepo.New(db.Queries)
	e := mustSeedEventForParticipants(t, db, "crparts")

	r, err := calModel.NewEventReservation(calModel.EventInput{
		EventID: e.ID, Window: rcWindow(0, 4), Teams: 2, PerTeam: calModel.Amount{CPUMillicores: 500, MemoryBytes: 1 << 29},
	}, e.CreatedBy.UUID, rcT0)
	require.NoError(t, err)
	agentA, agentB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	r.SetPlacement([]calModel.Share{{AgentID: agentA, Units: 1}, {AgentID: agentB, Units: 1}}, 0, rcT0)
	require.NoError(t, repo.CreateReservation(ctx, r))

	_, add, _ := calModel.SplitCalendar(r, nil, func() uuid.UUID { return uuid.Must(uuid.NewV7()) }, rcT0)
	require.Len(t, add, 2)
	for _, p := range add {
		require.NoError(t, repo.SavePart(ctx, p))
	}
	parts, err := repo.PartsOfCalendar(ctx, r.ID)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.Equal(t, add[0].Amount, parts[0].Amount)
	assert.Equal(t, r.Window, parts[0].Window)
	assert.False(t, parts[0].Synced())

	dup := calModel.NewPart(uuid.Must(uuid.NewV7()), agentA, calModel.PartCalendar, r.Window, add[0].Amount, rcT0)
	dup.CalendarID = &r.ID
	assert.Error(t, repo.SavePart(ctx, dup), "one live part per calendar reservation and agent")

	p := parts[0]
	p.SetDesired(calModel.Window{Start: r.Window.Start, End: r.Window.End.Add(time.Hour)}, p.Amount, rcT0)
	require.NoError(t, repo.SavePart(ctx, p))
	require.NoError(t, repo.MarkSynced(ctx, p.ID, calModel.SyncAnswer{OK: true, Rev: 1, At: rcT0}))
	got, err := repo.GetPart(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.DesiredRev)
	assert.Equal(t, int64(1), got.SyncedRev)
	assert.False(t, got.Synced(), "an older confirmation leaves the newer revision unsynced")
	require.NoError(t, repo.MarkSynced(ctx, p.ID, calModel.SyncAnswer{OK: true, Rev: 2, At: rcT0}))
	require.NoError(t, repo.MarkSynced(ctx, p.ID, calModel.SyncAnswer{OK: true, Rev: 1, At: rcT0}))
	got, err = repo.GetPart(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.SyncedRev, "a confirmation never lowers the synced revision")

	notice := rcT0.Add(time.Hour)
	require.NoError(t, repo.Observe(ctx, p.ID, calModel.PartStatus{Phase: calModel.PartEnding, NoticeAt: &notice, EndsAt: &notice, EndingReason: "WindowClosing", Used: calModel.Amount{CPUMillicores: 3, MemoryBytes: 4}, Groups: 2, ObservedAt: &notice}))
	require.NoError(t, repo.SavePart(ctx, got))
	got, err = repo.GetPart(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, calModel.PartEnding, got.Status.Phase, "a desired write keeps the observed status")
	assert.Equal(t, 2, got.Status.Groups)

	unsynced, err := repo.UnsyncedAgents(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{parts[1].AgentID}, unsynced)

	won, err := repo.AssignUnit(ctx, r.ID, calModel.TeamUnit(e.ID), agentB, rcT0)
	require.NoError(t, err)
	assert.Equal(t, agentB, won)
	won, err = repo.AssignUnit(ctx, r.ID, calModel.TeamUnit(e.ID), agentA, rcT0)
	require.NoError(t, err)
	assert.Equal(t, agentB, won, "a unit keeps its first agent")
	units, err := repo.Units(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]uuid.UUID{calModel.TeamUnit(e.ID): agentB}, units)
	require.NoError(t, repo.ReleaseUnit(ctx, r.ID, calModel.TeamUnit(e.ID)))

	got.Retire(rcT0)
	require.NoError(t, repo.SavePart(ctx, got))
	live, err := repo.PartsOfCalendar(ctx, r.ID)
	require.NoError(t, err)
	assert.Len(t, live, 1)
	ended, err := repo.EndPart(ctx, got.ID, calModel.PartStatus{ObservedAt: &notice})
	require.NoError(t, err)
	assert.True(t, ended, "the first end changes the row")
	ended, err = repo.EndPart(ctx, got.ID, calModel.PartStatus{ObservedAt: &notice})
	require.NoError(t, err)
	assert.False(t, ended, "a second end changes nothing: only one caller acts")
	require.NoError(t, repo.Observe(ctx, got.ID, calModel.PartStatus{Phase: calModel.PartActive, ObservedAt: &notice}))
	got, err = repo.GetPart(ctx, got.ID)
	require.NoError(t, err)
	assert.Equal(t, calModel.PartEnded, got.Status.Phase, "a late status does not undo the end")
	require.NoError(t, repo.DeletePart(ctx, got.ID))
}

func TestClusterReservationsMigration186RoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"0186_cluster_reservations.down.sql", "0186_cluster_reservations.up.sql"} {
		data, err := os.ReadFile(filepath.Join("migrations", name))
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, string(data))
		require.NoError(t, err, name)
	}
	var exists bool
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='cluster_reservation_units')`).Scan(&exists))
	assert.True(t, exists)
}
