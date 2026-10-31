package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/dashboardRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestAdminDashboardQueries(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := dashboardRepo.New(db.Queries)
	f := anSeed(t, db, "dash")
	now := anStart.Add(time.Hour) // the event started an hour ago and runs for four
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true, archive_at = NULL WHERE id = $1`, f.event)
	rtExec(t, db, `INSERT INTO event_team_stands (event_team_id, event_id, status, generation, reason, created_at, updated_at, status_changed_at) VALUES ($1, $2, 3, 1, 'boom', $3, $3, $3)`,
		f.team, f.event, anStart)

	failed, err := repo.FailedStands(ctx, now, 5)
	require.NoError(t, err)
	require.EqualValues(t, 1, failed.Total)
	require.Len(t, failed.Items, 1)
	require.Equal(t, f.event, failed.Items[0].ID)

	missing, err := repo.EventsWithoutReservation(ctx, now, now.Add(14*24*time.Hour), 5)
	require.NoError(t, err)
	require.EqualValues(t, 1, missing.Total, "a running stand event with no reservation")
	rtExec(t, db, `INSERT INTO resource_reservations (id, kind, event_id, starts_at, ends_at, teams, per_team_cpu_millicores, per_team_memory_bytes,
		largest_device_cpu_millicores, largest_device_memory_bytes, buffer_percent, dynamic_cpu_millicores, dynamic_memory_bytes, tail_gap_seconds,
		size_cpu_millicores, size_memory_bytes, placement, unplaced, per_team_snapshot_quota_bytes, dynamic_snapshot_quota_bytes, size_snapshot_quota_bytes, created_at, updated_at)
		VALUES (gen_random_uuid(), 'event', $1, $2, $3, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, '[]'::jsonb, 0, 0, 0, 0, now(), now())`, f.event, anStart.Add(-time.Hour), anStart.Add(6*time.Hour))
	missing, err = repo.EventsWithoutReservation(ctx, now, now.Add(14*24*time.Hour), 5)
	require.NoError(t, err)
	require.Zero(t, missing.Total, "reserved now")

	current, err := repo.CurrentEvents(ctx, now, now.Add(7*24*time.Hour), 20)
	require.NoError(t, err)
	require.Len(t, current, 1)
	require.True(t, current[0].Running)
	require.EqualValues(t, 1, current[0].Participants)
	require.EqualValues(t, 1, current[0].StandsTotal)
	require.EqualValues(t, 0, current[0].StandsReady)

	days, err := repo.Trends(ctx, now.Add(-6*24*time.Hour), 7)
	require.NoError(t, err)
	require.Len(t, days, 7, "a point for every day, empty days included")

	total, oldest, err := repo.PendingElevations(ctx)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Nil(t, oldest)
	errors, err := repo.OpenErrors(ctx, 5)
	require.NoError(t, err)
	require.Zero(t, errors.Total)
	mail, err := repo.MailErrors(ctx, now.Add(-7*24*time.Hour))
	require.NoError(t, err)
	require.Zero(t, mail)
}
