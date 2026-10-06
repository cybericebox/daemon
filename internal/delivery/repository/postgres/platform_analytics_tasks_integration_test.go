package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// paTask describes the seeded challenge as a published catalog task.
func paTask(t *testing.T, db *testhelpers.TestDB, f anFixture, name, difficulty string) {
	t.Helper()
	rtExec(t, db, `UPDATE event_challenges SET published = true, snapshot = jsonb_build_object('name', $2::text, 'difficulty', $3::text) WHERE id = $1`,
		f.challenge, name, difficulty)
	// Catalog names are unique: the exercise takes the task's name.
	rtExec(t, db, `UPDATE exercises SET name = $2 WHERE id = (SELECT ee.exercise_id FROM event_exercises ee JOIN event_challenges ec ON ec.event_exercise_id = ee.id WHERE ec.id = $1)`,
		f.challenge, name)
}

// Catalog usage and calibration: the attempts, solves, hint unlocks and the
// solve time of a task across events, without the moderators team, filtered
// by category and level and scoped by the period.
func TestPlatformAnalytics_TaskCatalog(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)

	f := anSeed(t, db, "patasks")
	paTask(t, db, f, "Login bypass", "easy")
	// One wrong attempt, then the solve; the team opened the task 30 s in.
	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(7*time.Minute))
	paSolve(t, db, f.teamChallenge, anStart.Add(7*time.Minute))
	rtExec(t, db, `INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at) VALUES ($1, $2, $3, 'task_opened', $4, $5)`,
		f.event, f.user, f.team, f.challenge, anStart.Add(30*time.Second))
	rtExec(t, db, `INSERT INTO team_challenge_hint_unlocks (team_challenge_id, hint_id, event_id, event_team_id, event_challenge_id, unlocked_at, cost)
VALUES ($1, $2, $3, $4, $5, $6, 5)`, f.teamChallenge, uuid.Must(uuid.NewV7()), f.event, f.team, f.challenge, anStart.Add(3*time.Minute))
	// The moderators team's attempts and solve are not counted.
	anAttempt(t, db, f, f.moderators, f.modChallenge, true, anStart.Add(2*time.Minute))
	paSolve(t, db, f.modChallenge, anStart.Add(2*time.Minute))

	// A second event uses another task that nobody solved.
	g := anSeed(t, db, "patasks2")
	paTask(t, db, g, "Hidden flag", "hard")
	anAttempt(t, db, g, g.team, g.teamChallenge, false, anStart.Add(time.Minute))

	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	from, to := day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)

	rows, total, err := repo.TaskCatalog(ctx, from, to, "", "", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("catalog = %+v (total %d)", rows, total)
	}
	byName := map[string]platformAnalyticsRepo.CatalogTask{}
	for _, r := range rows {
		byName[r.TaskName] = r
	}
	solved := byName["Login bypass"]
	if solved.EventsUsed != 1 || solved.Attempts != 2 || solved.TeamsTried != 1 || solved.TeamsEngaged != 1 || solved.Solves != 1 || solved.TeamsHinted != 1 {
		t.Fatalf("solved task = %+v", solved)
	}
	if solved.MedianSolveSeconds == nil || *solved.MedianSolveSeconds != 390 {
		t.Fatalf("median = %v, want 390 s (from the first open)", solved.MedianSolveSeconds)
	}
	if solved.Difficulty != "easy" || len(solved.Tags) != 1 || solved.Tags[0] != "web" {
		t.Fatalf("solved task identity = %+v", solved)
	}
	unsolved := byName["Hidden flag"]
	if unsolved.Solves != 0 || unsolved.Attempts != 1 || unsolved.MedianSolveSeconds != nil || unsolved.TeamsHinted != 0 {
		t.Fatalf("unsolved task = %+v", unsolved)
	}

	// Filters.
	if rows, total, err = repo.TaskCatalog(ctx, from, to, "web", "hard", 5000); err != nil || total != 1 || len(rows) != 1 || rows[0].TaskName != "Hidden flag" {
		t.Fatalf("category+level filter = %+v, %d, %v", rows, total, err)
	}
	if rows, total, err = repo.TaskCatalog(ctx, from, to, "crypto", "", 5000); err != nil || len(rows) != 0 || total != 0 {
		t.Fatalf("unknown category = %+v, %d, %v", rows, total, err)
	}
	// The row limit bounds the result, the total stays.
	if rows, total, err = repo.TaskCatalog(ctx, from, to, "", "", 1); err != nil || len(rows) != 1 || total != 2 {
		t.Fatalf("limit 1 = %+v, %d, %v", rows, total, err)
	}
	// Period: events that start outside the window are not counted.
	if rows, total, err = repo.TaskCatalog(ctx, day.AddDate(0, 0, -10), day.AddDate(0, 0, -8), "", "", 5000); err != nil || len(rows) != 0 || total != 0 {
		t.Fatalf("empty period = %+v, %d, %v", rows, total, err)
	}

	categories, err := repo.TaskCategories(ctx, from, to)
	if err != nil || len(categories) != 1 || categories[0] != "web" {
		t.Fatalf("categories = %v, %v", categories, err)
	}
	if categories, err = repo.TaskCategories(ctx, day.AddDate(0, 0, -10), day.AddDate(0, 0, -8)); err != nil || len(categories) != 0 {
		t.Fatalf("categories in an empty period = %v, %v", categories, err)
	}
}
