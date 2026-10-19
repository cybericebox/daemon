package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestExerciseVariantsAvgDifficulty covers the SQL average (migration 0184): the mean level of the subtasks of the
// FIRST variant, rounded to two decimals; NULL without variants, subtasks or known levels.
func TestExerciseVariantsAvgDifficulty(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		variants string
		want     *float64
	}{
		{name: "no variants", variants: `[]`},
		{name: "not an array", variants: `{}`},
		{name: "a variant without subtasks", variants: `[{"tasks":[]}]`},
		{name: "a variant with tasks null", variants: `[{"tasks":null}]`},
		{name: "no known level", variants: `[{"tasks":[{"difficulty":""},{"name":"x"},{"difficulty":"legendary"}]}]`},
		{name: "one subtask", variants: `[{"tasks":[{"difficulty":"medium"}]}]`, want: ptrTo(4.0)},
		{name: "every level", variants: `[{"tasks":[{"difficulty":"elementary"},{"difficulty":"trivial"},{"difficulty":"easy"},{"difficulty":"medium"},{"difficulty":"hard"},{"difficulty":"insane"}]}]`, want: ptrTo(3.5)},
		{name: "unknown levels are left out", variants: `[{"tasks":[{"difficulty":"hard"},{"difficulty":""},{"difficulty":"insane"}]}]`, want: ptrTo(5.5)},
		{name: "rounded to two decimals", variants: `[{"tasks":[{"difficulty":"easy"},{"difficulty":"easy"},{"difficulty":"medium"}]}]`, want: ptrTo(3.33)},
		{name: "only the first variant counts", variants: `[{"tasks":[{"difficulty":"trivial"}]},{"tasks":[{"difficulty":"insane"}]}]`, want: ptrTo(2.0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *float64
			if err := db.Pool.QueryRow(ctx, `SELECT exercise_variants_avg_difficulty($1::jsonb)::float8`, tc.variants).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("got %v, want %v", deref(got), deref(tc.want))
			}
		})
	}
}

func ptrTo(v float64) *float64 { return &v }

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// levels is one variant whose subtasks have the given levels.
func levels(ds ...exerciseModel.Difficulty) []exerciseModel.Variant {
	tasks := make([]exerciseModel.Task, 0, len(ds))
	for i, d := range ds {
		tasks = append(tasks, exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task " + string(rune('A'+i)), Description: json.RawMessage(`{"blocks":[]}`), Difficulty: d})
	}
	return []exerciseModel.Variant{{Index: 0, Tasks: tasks}}
}

// TestListExercisesPage_TableFilters covers the catalog table in SQL: the author name and filter, tags with all of
// and none of, several statuses (archived together with others), the average difficulty from the published version
// else the working copy and kept current on save, its range filter (no difficulty never passes a range) and sorts.
func TestListExercisesPage_TableFilters(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ada, bob := mustSeedUser(t, db, "ada@test.test"), mustSeedUser(t, db, "bob@test.test")
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET first_name = 'Ada', last_name = 'Lovelace' WHERE id = $1`, ada); err != nil {
		t.Fatal(err)
	}
	create := func(name string, tags []string, author uuid.UUID, at time.Time) exerciseModel.Exercise {
		t.Helper()
		e, err := exerciseModel.NewExercise(name, "", tags, author, at)
		if err != nil {
			t.Fatal(err)
		}
		created, err := repo.Create(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	save := func(id uuid.UUID, variants []exerciseModel.Variant, at time.Time) {
		t.Helper()
		if _, err := repo.UpsertDraft(ctx, id, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, at, uuid.NullUUID{}); err != nil {
			t.Fatal(err)
		}
	}
	easy := create("Easy web", []string{"web", "sqli"}, ada, itNow)
	save(easy.ID, levels(exerciseModel.DifficultyEasy, exerciseModel.DifficultyEasy), itNow)
	if _, err := repo.Publish(ctx, easy.ID, itNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The working copy is harder, the published version is what the catalog describes.
	save(easy.ID, levels(exerciseModel.DifficultyInsane), itNow.Add(2*time.Minute))
	hard := create("Hard crypto", []string{"crypto"}, bob, itNow.Add(time.Second))
	save(hard.ID, levels(exerciseModel.DifficultyHard, exerciseModel.DifficultyInsane), itNow) // draft only: 5.5 → 6
	_ = create("Legacy web", []string{"web", "legacy"}, ada, itNow.Add(2*time.Second))
	_ = create("No content", []string{"web"}, bob, itNow.Add(3*time.Second)) // no version: no difficulty
	archived := create("Old one", []string{"web"}, bob, itNow.Add(4*time.Second))
	if _, err := db.Pool.Exec(ctx, `UPDATE exercises SET archived_at = $1 WHERE id = $2`, itNow, archived.ID); err != nil {
		t.Fatal(err)
	}

	list := func(p exerciseRepo.PageParams) []exerciseRepo.Listed {
		t.Helper()
		if p.SortBy == "" {
			p.SortBy, p.SortDir = "name", "asc"
		}
		if p.Archived == "" {
			p.Archived = "exclude"
		}
		p.Limit = 50
		rows, err := repo.ListPage(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		count, err := repo.CountPage(ctx, p)
		if err != nil || count != int64(len(rows)) {
			t.Fatalf("count %d for %d rows: %v", count, len(rows), err)
		}
		return rows
	}
	names := func(rows []exerciseRepo.Listed) []string {
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = row.Name
		}
		return out
	}
	expect := func(label string, rows []exerciseRepo.Listed, want ...string) {
		t.Helper()
		got := names(rows)
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", label, got, want)
			}
		}
	}

	all := list(exerciseRepo.PageParams{})
	expect("everything active", all, "Easy web", "Hard crypto", "Legacy web", "No content")
	byName := map[string]exerciseRepo.Listed{}
	for _, row := range all {
		byName[row.Name] = row
	}
	if row := byName["Easy web"]; row.AuthorName != "Ada Lovelace" || row.AvgDifficulty == nil || *row.AvgDifficulty != 3 {
		t.Fatalf("published version describes the task: %+v", row)
	}
	if row := byName["Hard crypto"]; row.AuthorName != "" || row.AvgDifficulty == nil || *row.AvgDifficulty != 5.5 {
		t.Fatalf("an author without a name, the working copy of a draft-only task: %+v", row)
	}
	if row := byName["No content"]; row.AvgDifficulty != nil || row.TaskCount != nil {
		t.Fatalf("no version, no difficulty, no subtask count: %+v", row)
	}
	// Subtasks of the first variant of the published version (2), not of the working copy (1).
	if row := byName["Easy web"]; row.TaskCount == nil || *row.TaskCount != 2 {
		t.Fatalf("subtask count of the published version: %+v", row.TaskCount)
	}
	if row := byName["Hard crypto"]; row.TaskCount == nil || *row.TaskCount != 2 {
		t.Fatalf("subtask count of a draft-only task: %+v", row.TaskCount)
	}
	// Sorted by subtasks: none last in both directions; ties keep the newest first.
	expect("subtasks ascending", list(exerciseRepo.PageParams{SortBy: "subtasks", SortDir: "asc"}), "Hard crypto", "Easy web", "No content", "Legacy web")

	expect("author", list(exerciseRepo.PageParams{AuthorIDs: []uuid.UUID{ada}}), "Easy web", "Legacy web")
	expect("authors", list(exerciseRepo.PageParams{AuthorIDs: []uuid.UUID{ada, bob}}), "Easy web", "Hard crypto", "Legacy web", "No content")
	expect("all of the tags", list(exerciseRepo.PageParams{TagsAll: []string{"web", "sqli"}}), "Easy web")
	expect("none of the tags", list(exerciseRepo.PageParams{TagsAll: []string{"web"}, TagsNone: []string{"legacy", "sqli"}}), "No content")
	expect("any of the tags (legacy)", list(exerciseRepo.PageParams{Tags: []string{"crypto", "legacy"}}), "Hard crypto", "Legacy web")
	expect("several statuses", list(exerciseRepo.PageParams{Statuses: []string{"changed", "draft_only"}}), "Easy web", "Hard crypto")
	expect("archived with others", list(exerciseRepo.PageParams{Statuses: []string{"archived", "none"}, Archived: "any"}), "Legacy web", "No content", "Old one")
	expect("archived only", list(exerciseRepo.PageParams{Statuses: []string{"archived"}, Archived: "only"}), "Old one")
	// Rounded levels: Easy web 3 (easy), Hard crypto 5.5 → 6 (insane).
	expect("easy..hard", list(exerciseRepo.PageParams{DifficultyMin: 3, DifficultyMax: 5}), "Easy web")
	expect("from insane", list(exerciseRepo.PageParams{DifficultyMin: 6}), "Hard crypto")
	expect("up to medium", list(exerciseRepo.PageParams{DifficultyMax: 4}), "Easy web")
	// Ties (no difficulty) keep the newest first.
	expect("difficulty ascending, none last", list(exerciseRepo.PageParams{SortBy: "difficulty", SortDir: "asc"}), "Easy web", "Hard crypto", "No content", "Legacy web")
	desc := list(exerciseRepo.PageParams{SortBy: "difficulty", SortDir: "desc"})
	if got := names(desc); got[0] != "Hard crypto" || got[1] != "Easy web" {
		t.Fatalf("difficulty descending, none last: %v", got)
	}
	byAuthor := names(list(exerciseRepo.PageParams{SortBy: "author", SortDir: "desc"}))
	if byAuthor[0] != "Easy web" && byAuthor[0] != "Legacy web" || byAuthor[3] != "Hard crypto" && byAuthor[3] != "No content" {
		t.Fatalf("by author, unnamed last: %v", byAuthor)
	}

	// The generated column follows every save of the working copy.
	save(hard.ID, levels(exerciseModel.DifficultyElementary), itNow.Add(time.Hour))
	expect("kept current", list(exerciseRepo.PageParams{DifficultyMax: 1}), "Hard crypto")
	if rows := list(exerciseRepo.PageParams{DifficultyMax: 1}); rows[0].TaskCount == nil || *rows[0].TaskCount != 1 {
		t.Fatalf("subtask count follows the working copy: %+v", rows[0].TaskCount)
	}

	authors, err := repo.ListAuthors(ctx, "", nil, uuid.NullUUID{}, 10)
	if err != nil || len(authors) != 2 || authors[0].ID != bob || authors[0].Count != 3 || authors[1].Name != "Ada Lovelace" {
		t.Fatalf("authors by use: %+v err=%v", authors, err)
	}
	found, err := repo.ListAuthors(ctx, "love", nil, uuid.NullUUID{}, 10)
	if err != nil || len(found) != 1 || found[0].ID != ada {
		t.Fatalf("search by name: %+v err=%v", found, err)
	}
	chosen, err := repo.ListAuthors(ctx, "", []uuid.UUID{ada}, uuid.NullUUID{}, 10)
	if err != nil || len(chosen) != 1 || chosen[0].ID != ada || chosen[0].Name != "Ada Lovelace" {
		t.Fatalf("by ids: %+v err=%v", chosen, err)
	}
}

// TestListExercisesPage_SubtaskCountEdgeCases: a variant without subtasks counts 0, a version without variants has
// no count, and only the first variant counts.
func TestListExercisesPage_SubtaskCountEdgeCases(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	for _, tc := range []struct {
		name     string
		variants []exerciseModel.Variant
		want     *int
	}{
		{name: "no variants", variants: []exerciseModel.Variant{}},
		{name: "empty variant", variants: []exerciseModel.Variant{{Index: 0}}, want: intPtr(0)},
		{name: "first variant only", variants: append(levels(exerciseModel.DifficultyEasy, exerciseModel.DifficultyHard, exerciseModel.DifficultyMedium),
			levels(exerciseModel.DifficultyEasy)[0]), want: intPtr(3)},
	} {
		e := mustCreateExerciseFull(t, repo, tc.name, "", nil, itNow)
		if _, err := repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: tc.variants}, itNow, uuid.NullUUID{}); err != nil {
			t.Fatal(err)
		}
		rows, err := repo.ListPage(ctx, exerciseRepo.PageParams{Search: tc.name, Archived: "exclude", SortBy: "name", SortDir: "asc", Limit: 5})
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: rows=%d err=%v", tc.name, len(rows), err)
		}
		got := rows[0].TaskCount
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Fatalf("%s: count %v, want %v", tc.name, got, tc.want)
		}
	}
}

func intPtr(v int) *int { return &v }

// TestListAuthors_ViewerSeesOnlyReadable: a moderator learns only the authors of tasks they may read.
func TestListAuthors_ViewerSeesOnlyReadable(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "authors")
	moderator, insider, outsider := mustSeedUser(t, db, "mod@test.test"), mustSeedUser(t, db, "in@test.test"), mustSeedUser(t, db, "out@test.test")
	if _, err := db.Queries.CreateEventManager(ctx, postgres.CreateEventManagerParams{EventID: event.ID, UserID: moderator, Role: 1, CreatedAt: itNow}); err != nil {
		t.Fatal(err)
	}
	own, err := exerciseModel.NewEventExercise("Own task", "", nil, event.ID, insider, itNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Create(ctx, own); err != nil {
		t.Fatal(err)
	}
	private, err := exerciseModel.NewExercise("Private catalog task", "", nil, outsider, itNow) // never published
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Create(ctx, private); err != nil {
		t.Fatal(err)
	}
	authors, err := repo.ListAuthors(ctx, "", nil, uuid.NullUUID{UUID: moderator, Valid: true}, 10)
	if err != nil || len(authors) != 1 || authors[0].ID != insider {
		t.Fatalf("a moderator sees the authors of what they may read only: %+v err=%v", authors, err)
	}
}
