package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestExerciseCatalogList covers the catalog list in SQL: the derived status
// (exercise_status, incl. a working copy re-saved with the published
// content), the status filter and sort, several events at once with correct
// paging, the manager view of catalog working copies, the batched selected
// events and the top tags.
func TestExerciseCatalogList(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	eventA := mustSeedEventForParticipants(t, db, "lista")
	eventB := mustSeedEventForParticipants(t, db, "listb")
	eventC := mustSeedEventForParticipants(t, db, "listc")
	manager := mustSeedUser(t, db, "list-manager@test.test")
	if _, err := db.Queries.CreateEventManager(ctx, postgres.CreateEventManagerParams{EventID: eventA.ID, UserID: manager, Role: 1, CreatedAt: itNow}); err != nil {
		t.Fatal(err)
	}

	create := func(e exerciseModel.Exercise, err error) exerciseModel.Exercise {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		created, err := repo.Create(ctx, e)
		if err != nil {
			t.Fatalf("create %q: %v", e.Name, err)
		}
		return created
	}
	content := itVariants() // one fixed content: re-saving it is "no change"
	save := func(e exerciseModel.Exercise, note string) {
		t.Helper()
		if _, err := repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: note, Variants: content}, itNow, uuid.NullUUID{}); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(e exerciseModel.Exercise) {
		t.Helper()
		save(e, "")
		if _, err := repo.Publish(ctx, e.ID, itNow); err != nil {
			t.Fatal(err)
		}
	}

	published := create(exerciseModel.NewExercise("List published", "", []string{"web"}, uuid.Nil, itNow))
	changed := create(exerciseModel.NewExercise("List changed", "", []string{"web", "crypto"}, uuid.Nil, itNow))
	same := create(exerciseModel.NewExercise("List same content", "", []string{"web"}, uuid.Nil, itNow))
	draftOnly := create(exerciseModel.NewExercise("List draft only", "", []string{"secret"}, uuid.Nil, itNow))
	none := create(exerciseModel.NewExercise("List nothing", "", nil, uuid.Nil, itNow))
	selectedNew, err := exerciseModel.NewExercise("List selected", "", nil, uuid.Nil, itNow)
	selectedNew.AccessLevel = exerciseModel.AccessSelectedEvents
	selected := create(selectedNew, err)
	archived := create(exerciseModel.NewExercise("List archived", "", nil, uuid.Nil, itNow))
	ownA := create(exerciseModel.NewEventExercise("List own A", "", nil, eventA.ID, uuid.Nil, itNow))
	ownB := create(exerciseModel.NewEventExercise("List own B", "", nil, eventB.ID, uuid.Nil, itNow))

	for _, e := range []exerciseModel.Exercise{published, changed, same, selected, archived, ownA, ownB} {
		publish(e)
	}
	save(changed, "edited")
	save(ownA, "edited")
	save(same, "")
	save(draftOnly, "")
	if err = repo.ReplaceAccessEvents(ctx, selected.ID, []uuid.UUID{eventB.ID, eventC.ID}); err != nil {
		t.Fatal(err)
	}
	if archived, err = repo.GetByID(ctx, archived.ID); err != nil {
		t.Fatal(err)
	}
	loadedAt := archived.UpdatedAt
	archived.Archive(uuid.Nil, itNow.Add(time.Minute))
	if affected, updateErr := repo.Update(ctx, archived, loadedAt); updateErr != nil || affected != 1 {
		t.Fatalf("archive: %d err=%v", affected, updateErr)
	}

	admin := exerciseRepo.Visibility{}
	asManager := exerciseRepo.Visibility{ViewerID: uuid.NullUUID{UUID: manager, Valid: true}}
	list := func(status, archivedFilter string, v exerciseRepo.Visibility) map[uuid.UUID]exerciseModel.CatalogStatus {
		t.Helper()
		rows, listErr := repo.ListPage(ctx, exerciseRepo.PageParams{Tags: []string{}, Status: status, Archived: archivedFilter, SortBy: "name", SortDir: "asc", Limit: 50, Visibility: v})
		if listErr != nil {
			t.Fatal(listErr)
		}
		count, countErr := repo.CountPage(ctx, "", []string{}, status, archivedFilter, v)
		if countErr != nil || count != int64(len(rows)) {
			t.Fatalf("count %d != rows %d (err=%v)", count, len(rows), countErr)
		}
		out := map[uuid.UUID]exerciseModel.CatalogStatus{}
		for _, row := range rows {
			out[row.ID] = row.Status
		}
		return out
	}
	expect := func(name string, got map[uuid.UUID]exerciseModel.CatalogStatus, want ...uuid.UUID) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %d rows %v, want %d", name, len(got), got, len(want))
		}
		for _, id := range want {
			if _, ok := got[id]; !ok {
				t.Fatalf("%s: missing %s in %v", name, id, got)
			}
		}
	}

	all := list("", "exclude", admin)
	for id, want := range map[uuid.UUID]exerciseModel.CatalogStatus{
		published.ID: exerciseModel.StatusPublished, changed.ID: exerciseModel.StatusChanged, same.ID: exerciseModel.StatusPublished,
		draftOnly.ID: exerciseModel.StatusDraftOnly, none.ID: exerciseModel.StatusNone, ownA.ID: exerciseModel.StatusChanged,
	} {
		if all[id] != want {
			t.Fatalf("status of %s = %q, want %q", id, all[id], want)
		}
	}
	expect("changed", list("changed", "exclude", admin), changed.ID, ownA.ID)
	expect("draft_only", list("draft_only", "exclude", admin), draftOnly.ID)
	expect("published", list("published", "exclude", admin), published.ID, same.ID, selected.ID, ownB.ID)
	expect("none", list("none", "exclude", admin), none.ID)
	expect("archived", list("archived", "only", admin), archived.ID)

	// A manager reads catalog exercises published-only: no working copy, no
	// "changed"; their own event exercise keeps its real status.
	mine := list("", "exclude", asManager)
	if mine[changed.ID] != exerciseModel.StatusPublished || mine[ownA.ID] != exerciseModel.StatusChanged {
		t.Fatalf("manager statuses: %v", mine)
	}
	expect("manager changed", list("changed", "exclude", asManager), ownA.ID)
	expect("manager draft_only", list("draft_only", "exclude", asManager))

	// Several events: scope event = owned by any; catalog = available to any.
	events := func(scope string, ids ...uuid.UUID) exerciseRepo.Visibility {
		return exerciseRepo.Visibility{Scope: scope, EventIDs: ids}
	}
	expect("owned by A or B", list("", "exclude", events("event", eventA.ID, eventB.ID)), ownA.ID, ownB.ID)
	expect("available to C", list("", "exclude", events("catalog", eventC.ID)), published.ID, changed.ID, same.ID, draftOnly.ID, none.ID, selected.ID)
	expect("union for B and C", list("", "exclude", events("", eventB.ID, eventC.ID)), published.ID, changed.ID, same.ID, draftOnly.ID, none.ID, selected.ID, ownB.ID)

	// Paging over several events: every row once, in the requested order.
	seen := map[uuid.UUID]bool{}
	for offset := int32(0); offset < 7; offset += 2 {
		rows, pageErr := repo.ListPage(ctx, exerciseRepo.PageParams{Tags: []string{}, Archived: "exclude", SortBy: "status", SortDir: "asc", Limit: 2, Offset: offset,
			Visibility: events("", eventB.ID, eventC.ID)})
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		for _, row := range rows {
			if seen[row.ID] {
				t.Fatalf("row %s repeated across pages", row.ID)
			}
			seen[row.ID] = true
		}
		if offset == 0 && (len(rows) != 2 || rows[0].ID != none.ID || rows[1].ID != draftOnly.ID) {
			t.Fatalf("status asc starts with none, then draft_only: %+v", rows)
		}
	}
	if len(seen) != 7 {
		t.Fatalf("pages covered %d rows, want 7", len(seen))
	}

	access, err := repo.AccessEventsOf(ctx, []uuid.UUID{selected.ID, published.ID})
	if err != nil {
		t.Fatal(err)
	}
	refs := access[selected.ID]
	if len(refs) != 2 || refs[0].ID != eventB.ID || refs[1].ID != eventC.ID || refs[0].Name == "" || len(access[published.ID]) != 0 {
		t.Fatalf("selected events by name: %+v", access)
	}

	tags, err := repo.ListTags(ctx, "", uuid.NullUUID{}, 50)
	if err != nil || len(tags) != 3 || tags[0].Tag != "web" || tags[0].Count != 3 || tags[1].Tag != "crypto" || tags[2].Tag != "secret" {
		t.Fatalf("top tags: %+v err=%v", tags, err)
	}
	if tags, err = repo.ListTags(ctx, "", uuid.NullUUID{}, 1); err != nil || len(tags) != 1 || tags[0].Tag != "web" {
		t.Fatalf("limited top tags: %+v err=%v", tags, err)
	}
	if tags, err = repo.ListTags(ctx, "", asManager.ViewerID, 50); err != nil || len(tags) != 2 || tags[0].Tag != "web" || tags[1].Tag != "crypto" {
		t.Fatalf("a manager counts only readable exercises: %+v err=%v", tags, err)
	}
}

// TestExerciseAccessNone: access level 3 passes the CHECK constraint and no
// event may use the exercise — availability, manager reads, the event catalog
// and the event list filter all refuse it; admins still list it.
func TestExerciseAccessNone(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "nonea")
	manager := mustSeedUser(t, db, "none-manager@test.test")
	if _, err := db.Queries.CreateEventManager(ctx, postgres.CreateEventManagerParams{EventID: event.ID, UserID: manager, Role: 1, CreatedAt: itNow}); err != nil {
		t.Fatal(err)
	}
	e, err := exerciseModel.NewExercise("Nobody may use", "", nil, uuid.Nil, itNow)
	if err != nil {
		t.Fatal(err)
	}
	if e, err = repo.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Publish(ctx, e.ID, itNow); err != nil {
		t.Fatal(err)
	}
	if e, err = repo.GetByID(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	loadedAt := e.UpdatedAt
	if err = e.SetAccess(exerciseModel.AccessNone, false, uuid.Nil, itNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if affected, updateErr := repo.Update(ctx, e, loadedAt); updateErr != nil || affected != 1 {
		t.Fatalf("store access none: %d err=%v", affected, updateErr)
	}
	if stored, getErr := repo.GetByID(ctx, e.ID); getErr != nil || stored.AccessLevel != exerciseModel.AccessNone {
		t.Fatalf("round trip: %+v err=%v", stored.AccessLevel, getErr)
	}

	if available, availErr := repo.AvailableToEvent(ctx, e.ID, event.ID); availErr != nil || available {
		t.Fatalf("available=%t err=%v", available, availErr)
	}
	if readable, readErr := repo.ReadableBy(ctx, e.ID, manager); readErr != nil || readable {
		t.Fatalf("readable=%t err=%v", readable, readErr)
	}
	catalog, err := db.Queries.ListEventCatalog(ctx, postgres.ListEventCatalogParams{EventID: event.ID})
	if err != nil || len(catalog) != 0 {
		t.Fatalf("event catalog: %+v err=%v", catalog, err)
	}
	byEvent, err := repo.ListPage(ctx, exerciseRepo.PageParams{Tags: []string{}, Archived: "exclude", SortBy: "name", SortDir: "asc", Limit: 10,
		Visibility: exerciseRepo.Visibility{EventIDs: []uuid.UUID{event.ID}}})
	if err != nil || len(byEvent) != 0 {
		t.Fatalf("event filter: %d err=%v", len(byEvent), err)
	}
	all, err := repo.ListPage(ctx, exerciseRepo.PageParams{Tags: []string{}, Archived: "exclude", SortBy: "name", SortDir: "asc", Limit: 10})
	if err != nil || len(all) != 1 || all[0].Status != exerciseModel.StatusPublished {
		t.Fatalf("admin list: %+v err=%v", all, err)
	}

	if _, err = db.Pool.Exec(ctx, `UPDATE exercises SET access_level = 4 WHERE id = $1`, e.ID); err == nil {
		t.Fatal("access_level 4 must violate the check constraint")
	}
}
