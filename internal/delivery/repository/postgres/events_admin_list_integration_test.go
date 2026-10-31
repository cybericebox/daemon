package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The admin events table: counters of the page in one query, filters by start period, organizer and
// infrastructure, and sorting by the counters; the count follows the same filters.
func TestEventsAdminList_CountersFiltersAndSorting(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)
	big := anSeed(t, db, "adminbig")
	small := mustSeedEventForParticipants(t, db, "adminsmall")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, big.event)
	rtExec(
		t,
		db,
		`UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		small.ID,
		anStart.Add(-24*time.Hour),
		anStart.AddDate(0, 0, 30),
		anStart.AddDate(0, 0, 30).Add(4*time.Hour),
		anStart.AddDate(0, 3, 0),
	)
	rtExec(
		t,
		db,
		`INSERT INTO event_team_stands (event_team_id, event_id, status, generation, created_at, updated_at, status_changed_at) VALUES ($1, $2, 2, 1, $3, $3, $3)`,
		big.team,
		big.event,
		anStart,
	)

	counts, err := repo.CountsOf(ctx, []uuid.UUID{big.event, small.ID})
	require.NoError(t, err)
	require.EqualValues(t, 1, counts[big.event].Participants)
	require.EqualValues(t, 1, counts[big.event].Teams, "the moderators team is not counted")
	require.EqualValues(t, 1, counts[big.event].StandsReady)
	require.EqualValues(t, 1, counts[big.event].StandsTotal)
	require.EqualValues(t, 0, counts[small.ID].Participants)

	page := func(p eventRepo.PageParams) []uuid.UUID {
		p.Limit, p.Now = 50, anStart.Add(time.Hour)
		rows, listErr := repo.ListPage(ctx, p)
		require.NoError(t, listErr)
		total, countErr := repo.CountPage(ctx, p)
		require.NoError(t, countErr)
		require.EqualValues(t, len(rows), total, "the count follows the same filters")
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids
	}
	search := "admin"
	require.ElementsMatch(
		t,
		[]uuid.UUID{big.event, small.ID},
		page(eventRepo.PageParams{Search: search}),
	)
	require.Equal(
		t,
		[]uuid.UUID{big.event},
		page(eventRepo.PageParams{Search: search, Infrastructure: "true"}),
	)
	require.Equal(
		t,
		[]uuid.UUID{small.ID},
		page(eventRepo.PageParams{Search: search, Infrastructure: "false"}),
	)
	from, to := anStart.AddDate(0, 0, 10), anStart.AddDate(0, 0, 40)
	require.Equal(
		t,
		[]uuid.UUID{small.ID},
		page(eventRepo.PageParams{Search: search, StartFrom: &from, StartTo: &to}),
		"start period",
	)
	early := anStart.Add(time.Hour)
	require.Equal(
		t,
		[]uuid.UUID{big.event},
		page(eventRepo.PageParams{Search: search, StartTo: &early}),
	)
	var owner uuid.UUID
	require.NoError(
		t,
		db.Pool.QueryRow(ctx, `SELECT created_by FROM events WHERE id = $1`, small.ID).Scan(&owner),
	)
	rtExec(
		t,
		db,
		`INSERT INTO event_managers (event_id, user_id, role, created_at) VALUES ($1, $2, 0, now())`,
		small.ID,
		owner,
	)
	require.Equal(
		t,
		[]uuid.UUID{small.ID},
		page(
			eventRepo.PageParams{
				Search:      search,
				OrganizerID: uuid.NullUUID{UUID: owner, Valid: true},
			},
		),
		"organizer",
	)
	require.Equal(
		t,
		[]uuid.UUID{big.event, small.ID},
		page(eventRepo.PageParams{Search: search, SortBy: "participants", SortDir: "desc"}),
	)
	require.Equal(
		t,
		[]uuid.UUID{big.event, small.ID},
		page(eventRepo.PageParams{Search: search, SortBy: "start", SortDir: "asc"}),
	)
	require.Equal(
		t,
		[]uuid.UUID{small.ID, big.event},
		page(eventRepo.PageParams{Search: search, SortBy: "stands", SortDir: "asc"}),
	)
}

// "Needs a reservation" is a filter of the events table, paginated like the rest (no fixed cap).
func TestEventsAdminList_NeedsReservationFilter(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)
	f := anSeed(t, db, "needres")
	rtExec(
		t,
		db,
		`UPDATE events SET infrastructure_allowed = true, archive_at = NULL WHERE id = $1`,
		f.event,
	)
	params := eventRepo.PageParams{Limit: 50, Now: anStart.Add(time.Hour), NeedsReservation: "true"}

	rows, err := repo.ListPage(ctx, params)
	require.NoError(t, err)
	total, err := repo.CountPage(ctx, params)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 1, total)

	rtExec(
		t,
		db,
		`INSERT INTO resource_reservations (id, kind, event_id, starts_at, ends_at, teams, per_team_cpu_millicores, per_team_memory_bytes,
		largest_device_cpu_millicores, largest_device_memory_bytes, buffer_percent, dynamic_cpu_millicores, dynamic_memory_bytes, tail_gap_seconds,
		size_cpu_millicores, size_memory_bytes, placement, unplaced, per_team_snapshot_quota_bytes, dynamic_snapshot_quota_bytes, size_snapshot_quota_bytes, created_at, updated_at)
		VALUES (gen_random_uuid(), 'event', $1, $2, $3, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, '[]'::jsonb, 0, 0, 0, 0, now(), now())`,
		f.event,
		anStart.Add(-time.Hour),
		anStart.Add(6*time.Hour),
	)
	rows, err = repo.ListPage(ctx, params)
	require.NoError(t, err)
	total, err = repo.CountPage(ctx, params)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total)
}

// The stand table sorts by every column it shows.
func TestPlatformStandsListSorting(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "standsort")
	other := eaTeam(t, db, f.event, mustSeedUser(t, db, "standsort-other@test.test"), "Alpha")
	for _, team := range []uuid.UUID{f.team, other} {
		rtExec(
			t,
			db,
			`INSERT INTO event_team_stands (event_team_id, event_id, status, generation, created_at, updated_at, status_changed_at) VALUES ($1, $2, 2, 1, $3, $3, $3)`,
			team,
			f.event,
			anStart,
		)
	}
	rtExec(t, db, `UPDATE event_team_stands SET generation = 5 WHERE event_team_id = $1`, f.team)
	names := func(by, dir string) []string {
		rows, _, err := platformStandRepo.New(db.Queries).
			List(ctx, platformStandRepo.Filter{SortBy: by, SortDir: dir, Limit: 10})
		require.NoError(t, err)
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.TeamName)
		}
		return out
	}
	require.Equal(t, []string{"Alpha", "Blue"}, names("team", "asc"))
	require.Equal(t, []string{"Blue", "Alpha"}, names("team", "desc"))
	require.Equal(t, []string{"Blue", "Alpha"}, names("generation", "desc"))
}
