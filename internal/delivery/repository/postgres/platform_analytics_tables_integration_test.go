package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The people and event tables of the platform analytics search, sort and page on the server; the total follows the
// search, and a page of everything (the export) is not cut.
func TestPlatformAnalyticsTables_SearchSortAndPage(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "pat")
	second := mustSeedUser(t, db, "pat-second@test.test")
	rtExec(
		t,
		db,
		`INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 2, $3)`,
		f.event,
		second,
		anStart,
	)
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(time.Minute))
	rtExec(
		t,
		db,
		`INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, $2)`,
		f.teamChallenge,
		anStart.Add(time.Minute),
	)

	all, total, err := repo.UsersPeoplePage(
		ctx,
		platformAnalyticsRepo.TableQuery{SortBy: "email", SortDir: "asc", Limit: 2147483647},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, all, 2)
	require.Equal(t, "pat-member@test.test", all[0].Email)
	require.EqualValues(t, 1, all[0].Solves)

	bySolves, _, err := repo.UsersPeoplePage(
		ctx,
		platformAnalyticsRepo.TableQuery{SortBy: "solves", SortDir: "desc", Limit: 2147483647},
	)
	require.NoError(t, err)
	require.Equal(t, all[0].ID, bySolves[0].ID)

	page, total, err := repo.UsersPeoplePage(
		ctx,
		platformAnalyticsRepo.TableQuery{SortBy: "email", SortDir: "desc", Limit: 1, Offset: 1},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "the total is the whole list, not the page")
	require.Len(t, page, 1)
	require.Equal(t, "pat-member@test.test", page[0].Email)

	found, total, err := repo.UsersPeoplePage(
		ctx,
		platformAnalyticsRepo.TableQuery{Search: "second", Limit: 10},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, second, found[0].ID)

	from, to := anStart.Add(-time.Hour), anStart.Add(24*time.Hour)
	events, total, err := repo.EventsPage(
		ctx,
		from,
		to,
		anStart.Add(time.Hour),
		platformAnalyticsRepo.TableQuery{SortBy: "participants", SortDir: "desc", Limit: 10},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, events, 1)
	require.Equal(t, f.event, events[0].ID)
	require.EqualValues(t, 1, events[0].Solves)

	none, total, err := repo.EventsPage(
		ctx,
		from,
		to,
		anStart,
		platformAnalyticsRepo.TableQuery{Search: "no-such-event", Limit: 10},
	)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, none)
}
