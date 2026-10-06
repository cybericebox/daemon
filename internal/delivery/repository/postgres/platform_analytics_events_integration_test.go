package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func paSolve(t *testing.T, db *testhelpers.TestDB, teamChallenge uuid.UUID, at time.Time) {
	t.Helper()
	if err := db.Queries.UpsertTeamChallengeSolve(context.Background(), postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallenge, SolvedAt: at}); err != nil {
		t.Fatal(err)
	}
}

// The events section counts registrations without pending invitations and
// without the moderators team, the teams and solves of an event without the
// moderators team, and scopes events by the period.
func TestPlatformAnalytics_Events(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "paevents")

	// A pending invitation and a moderators-team member are not registrations.
	invited := mustSeedUser(t, db, "paevents-invited@test.test")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, invited) VALUES ($1, $2, 1, $3, true)`, f.event, invited, anStart)
	mod := mustSeedUser(t, db, "paevents-mod@test.test")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 1)`, f.event, mod, anStart, f.moderators)
	paSolve(t, db, f.teamChallenge, anStart.Add(10*time.Minute))
	paSolve(t, db, f.modChallenge, anStart.Add(10*time.Minute))

	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	from, to, asOf := day.AddDate(0, 0, -2), day.AddDate(0, 0, 2), anStart.Add(time.Hour)

	series, err := repo.EventSeries(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 4 {
		t.Fatalf("series has %d days, want 4", len(series))
	}
	var registrations, started int64
	for _, d := range series {
		registrations += d.Registrations
		started += d.EventsStarted
		if d.Registrations > 0 && !d.Day.Equal(day) {
			t.Fatalf("registrations on %v, want %v", d.Day, day)
		}
	}
	if registrations != 1 || started != 1 {
		t.Fatalf("registrations = %d, started = %d, want 1 and 1", registrations, started)
	}

	statuses, err := repo.EventStatuses(ctx, from, to, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Status != 2 || statuses[0].Events != 1 {
		t.Fatalf("statuses = %+v, want one started event", statuses)
	}

	rows, total, err := repo.Events(ctx, from, to, asOf, 200)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("events = %+v (total %d)", rows, total)
	}
	row := rows[0]
	if row.ID != f.event || row.Status != 2 || row.Participants != 1 || row.Teams != 1 || row.Solves != 1 || row.TeamsSolved != 1 {
		t.Fatalf("event row = %+v", row)
	}
	if row.FinishAt == nil || !row.FinishAt.Equal(anStart.Add(4*time.Hour)) || !row.StartAt.Equal(anStart) {
		t.Fatalf("event window = %v .. %v", row.StartAt, row.FinishAt)
	}
	if limited, all, err := repo.Events(ctx, from, to, asOf, 0); err != nil || len(limited) != 0 || all != 0 {
		t.Fatalf("limit 0 = %+v, %d, %v", limited, all, err)
	}
	// A finished event reads as finished after its finish.
	if finished, err := repo.EventStatuses(ctx, from, to, anStart.Add(5*time.Hour)); err != nil || len(finished) != 1 || finished[0].Status != 3 {
		t.Fatalf("statuses after finish = %+v, %v", finished, err)
	}

	// The period bounds: a window that ends before the start has no events and
	// the series is all zeros.
	early, earlyTo := day.AddDate(0, 0, -10), day.AddDate(0, 0, -8)
	if rows, total, err = repo.Events(ctx, early, earlyTo, asOf, 200); err != nil || len(rows) != 0 || total != 0 {
		t.Fatalf("empty period events = %+v, %d, %v", rows, total, err)
	}
	if series, err = repo.EventSeries(ctx, early, earlyTo); err != nil || len(series) != 2 {
		t.Fatalf("empty period series = %+v, %v", series, err)
	}
	for _, d := range series {
		if d.EventsCreated != 0 || d.EventsStarted != 0 || d.Registrations != 0 {
			t.Fatalf("empty period series has activity: %+v", d)
		}
	}
}

// Upcoming lists the scheduled events that have not started, nearest first,
// with the registrations so far; a started event is not upcoming.
func TestPlatformAnalytics_UpcomingEvents(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "paupcoming")

	upcoming, err := repo.UpcomingEvents(ctx, anStart.Add(-2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(upcoming) != 1 || upcoming[0].ID != f.event || upcoming[0].Registrations != 1 || !upcoming[0].Published || !upcoming[0].StartAt.Equal(anStart) {
		t.Fatalf("upcoming = %+v", upcoming)
	}
	if started, err := repo.UpcomingEvents(ctx, anStart.Add(time.Minute), 10); err != nil || len(started) != 0 {
		t.Fatalf("started event listed as upcoming: %+v, %v", started, err)
	}
	if limited, err := repo.UpcomingEvents(ctx, anStart.Add(-2*time.Hour), 0); err != nil || len(limited) != 0 {
		t.Fatalf("limit 0 = %+v, %v", limited, err)
	}
}
