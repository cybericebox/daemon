package platformAnalytics

import (
	"context"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
)

func TestGetTasks(t *testing.T) {
	median := int64(120)
	store := &catalogFakeStore{
		cats: []string{"crypto", "web"},
		tasks: []platformAnalyticsRepo.CatalogTask{
			{ExerciseID: uuid.Must(uuid.NewV7()), TaskID: uuid.Must(uuid.NewV7()), ExerciseName: "Web", TaskName: "Login", Difficulty: "easy", Tags: []string{"web"},
				EventsUsed: 3, Attempts: 40, TeamsTried: 10, TeamsEngaged: 12, Solves: 9, TeamsHinted: 3, MedianSolveSeconds: &median},
			{ExerciseID: uuid.Must(uuid.NewV7()), TaskID: uuid.Must(uuid.NewV7()), ExerciseName: "Crypto", TaskName: "RSA", Difficulty: "hard", Tags: []string{"crypto", "web"},
				EventsUsed: 1, Attempts: 5, TeamsTried: 5, TeamsEngaged: 5, Solves: 0},
			{ExerciseID: uuid.Must(uuid.NewV7()), TaskID: uuid.Must(uuid.NewV7()), ExerciseName: "Misc", TaskName: "Unused", Difficulty: "", EventsUsed: 1},
		},
	}
	u := newCatalogUseCase(store)
	v, err := u.GetTasks(context.Background(), nil, nil, " web ", "easy")
	if err != nil {
		t.Fatal(err)
	}
	if store.category != "web" || store.level != "easy" {
		t.Fatalf("filters = %q, %q", store.category, store.level)
	}
	if v.Totals.TasksUsed != 3 || v.Totals.Uses != 5 || v.Totals.Attempts != 45 || v.Totals.Solves != 9 || v.Totals.NeverSolved != 2 {
		t.Fatalf("totals = %+v", v.Totals)
	}
	// 9 solves of 15 team-task pairs that tried.
	if v.Totals.SolveRate != 0.6 {
		t.Fatalf("solve rate = %v", v.Totals.SolveRate)
	}
	login := v.Tasks[0]
	if login.SolveRate != 0.9 || login.HintRate != 0.25 || login.MedianSolveSeconds == nil || *login.MedianSolveSeconds != 120 || login.Calibration != "ok" {
		t.Fatalf("login row = %+v", login)
	}
	if len(v.Unsolved) != 2 || v.UnsolvedTotal != 2 || v.Unsolved[0].Task != "RSA" {
		t.Fatalf("unsolved = %+v", v.Unsolved)
	}
	if v.Tasks[2].Categories == nil || v.Tasks[2].Calibration != "unknown" {
		t.Fatalf("untagged row = %+v", v.Tasks[2])
	}
	if len(v.Categories) != 2 || len(v.Levels) != 6 || v.Levels[0] != "elementary" || v.Levels[5] != "insane" {
		t.Fatalf("options = %v / %v", v.Categories, v.Levels)
	}
	if len(v.ByCategory) != 2 || v.ByCategory[0].Category != "web" || v.ByCategory[0].Tasks != 2 || v.ByCategory[0].Uses != 4 {
		t.Fatalf("by category = %+v", v.ByCategory)
	}
	if len(v.ByLevel) != 6 || v.ByLevel[2].Level != "easy" || v.ByLevel[2].SolveRate != 0.9 || v.ByLevel[3].TeamsTried != 0 {
		t.Fatalf("by level = %+v", v.ByLevel)
	}
}

func TestGetTasksLongCategoryMatchesNothing(t *testing.T) {
	store := &catalogFakeStore{tasks: []platformAnalyticsRepo.CatalogTask{{TaskName: "x"}}}
	v, err := newCatalogUseCase(store).GetTasks(context.Background(), nil, nil, strings.Repeat("a", 200), "")
	if err != nil || len(v.Tasks) != 0 || store.calls != 0 || v.Categories == nil {
		t.Fatalf("view = %+v, calls = %d, err = %v", v, store.calls, err)
	}
}

func TestGetTasksCacheKeyHoldsFilters(t *testing.T) {
	store := &catalogFakeStore{}
	u := newCatalogUseCase(store)
	for _, level := range []string{"easy", "hard", "easy"} {
		if _, err := u.GetTasks(context.Background(), nil, nil, "", level); err != nil {
			t.Fatal(err)
		}
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want 2 (one per distinct filter)", store.calls)
	}
}
