package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Keyset sentinels for a first page — mirrors useCase/exercise/catalog.go's
// unexported cursorSentinelTime/maxUUID (kept local since this package tests
// the SQL directly, without going through the use case).
var (
	itCursorSentinelTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	itMaxUUID            = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
)

var itNow = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func itVariants() []exerciseModel.Variant {
	return []exerciseModel.Variant{{
		Index: 0,
		Tasks: []exerciseModel.Task{{
			ID:          uuid.Must(uuid.NewV7()),
			Name:        "Find the flag",
			Description: json.RawMessage(`{"blocks":[]}`),
			Difficulty:  exerciseModel.DifficultyEasy,
			Flag:        []string{"ICE{x}"},
		}},
	}}
}

func mustCreateExercise(t *testing.T, repo *exerciseRepo.Repository, name string) exerciseModel.Exercise {
	t.Helper()
	e, err := exerciseModel.NewExercise(name, "desc", []string{"web"}, uuid.Nil, itNow)
	if err != nil {
		t.Fatalf("NewExercise: %v", err)
	}
	created, err := repo.Create(context.Background(), e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return created
}

// mustCreateExerciseFull lets a test control name/description/tags/createdAt
// independently, needed to exercise ILIKE search, the tags && overlap filter
// and deterministic (created_at, id) keyset ordering side by side.
func mustCreateExerciseFull(t *testing.T, repo *exerciseRepo.Repository, name, description string, tags []string, createdAt time.Time) exerciseModel.Exercise {
	t.Helper()
	e, err := exerciseModel.NewExercise(name, description, tags, uuid.Nil, createdAt)
	if err != nil {
		t.Fatalf("NewExercise(%q): %v", name, err)
	}
	created, err := repo.Create(context.Background(), e)
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	return created
}

func TestListExerciseTags_CountsExistingExercisesByLiteralPrefix(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := exerciseRepo.New(db.Queries)
	mustCreateExerciseFull(t, repo, "Crypto one", "", []string{"crypto", "web"}, itNow)
	mustCreateExerciseFull(t, repo, "Crypto two", "", []string{"crypto", "cryptography"}, itNow)
	mustCreateExerciseFull(t, repo, "Other", "", []string{"web", "c%literal"}, itNow)
	items, err := repo.ListTags(context.Background(), "CR", uuid.NullUUID{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Tag != "crypto" || items[0].Count != 2 || items[1].Tag != "cryptography" || items[1].Count != 1 {
		t.Fatalf("prefix counts: %+v", items)
	}
	items, err = repo.ListTags(context.Background(), "c%", uuid.NullUUID{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Tag != "c%literal" {
		t.Fatalf("literal wildcard prefix: %+v", items)
	}
}

// TestExerciseArchivedAt_RoundTripAndListFilter drives archived_at through a
// real write/read round trip and confirms the archived list filter (cursor
// and offset pages, plus their counts) partitions active vs. archived rows.
func TestExerciseArchivedAt_RoundTripAndListFilter(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	active := mustCreateExercise(t, repo, "IT active exercise")
	archived := mustCreateExercise(t, repo, "IT archived exercise")

	expected := archived.UpdatedAt
	archivedAt := itNow.Add(time.Hour)
	archived.Archive(uuid.Nil, archivedAt)
	if affected, err := repo.Update(ctx, archived, expected); err != nil || affected != 1 {
		t.Fatalf("archive write: affected=%d err=%v", affected, err)
	}
	got, err := repo.GetByID(ctx, archived.ID)
	if err != nil || got.ArchivedAt == nil || !got.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("archived_at round-trip: %+v err=%v", got, err)
	}

	for _, tc := range []struct {
		archived string
		want     uuid.UUID
	}{{"exclude", active.ID}, {"only", archived.ID}} {
		cursor, err := repo.ListCursor(ctx, exerciseRepo.ListParams{Tags: []string{}, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, Limit: 10, Archived: tc.archived})
		if err != nil || len(cursor) != 1 || cursor[0].ID != tc.want {
			t.Fatalf("cursor archived=%s: %v err=%v", tc.archived, cursor, err)
		}
		if count, err := repo.Count(ctx, "", []string{}, tc.archived, exerciseRepo.Visibility{}); err != nil || count != 1 {
			t.Fatalf("count archived=%s: %d err=%v", tc.archived, count, err)
		}
		page, err := repo.ListPage(ctx, exerciseRepo.PageParams{Tags: []string{}, SortBy: "updated", SortDir: "desc", Limit: 10, Archived: tc.archived})
		if err != nil || len(page) != 1 || page[0].ID != tc.want {
			t.Fatalf("page archived=%s: %v err=%v", tc.archived, page, err)
		}
		if count, err := repo.CountPage(ctx, "", []string{}, "", tc.archived, exerciseRepo.Visibility{}); err != nil || count != 1 {
			t.Fatalf("page count archived=%s: %d err=%v", tc.archived, count, err)
		}
	}
}

// TestExerciseLifecycle_CTERoundTrip drives the full draft->publish->rollback
// family transition against the real SQL — partial indexes, pointer moves and
// JSONB round-trip included.
func TestExerciseLifecycle_CTERoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)

	ex := mustCreateExercise(t, repo, "IT lifecycle")

	// 1. Save a draft (insert branch).
	draftID := uuid.Must(uuid.NewV7())
	v := exerciseModel.ExerciseVersion{AdminNote: "first cut", Variants: itVariants()}
	saved, err := repo.UpsertDraft(ctx, ex.ID, draftID, v, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("UpsertDraft insert: %v", err)
	}
	if saved.ID != draftID || !saved.Status.IsDraft() || len(saved.Variants) != 1 {
		t.Fatalf("unexpected draft: %+v", saved)
	}
	got, err := repo.GetByID(ctx, ex.ID)
	if err != nil || !got.DraftVersionID.Valid || got.DraftVersionID.UUID != draftID {
		t.Fatalf("draft pointer not set: %+v err=%v", got, err)
	}

	// 2. Save again (update branch) — same row, new content.
	v.AdminNote = "second cut"
	saved2, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), v, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("UpsertDraft update: %v", err)
	}
	if saved2.ID != draftID || saved2.AdminNote != "second cut" {
		t.Fatalf("update branch must reuse the draft row: %+v", saved2)
	}

	// 3. Publish: draft -> published, pointers move.
	published, err := repo.Publish(ctx, ex.ID, itNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if published.ID != draftID || !published.Status.IsPublished() || published.PublishedAt == nil {
		t.Fatalf("unexpected published: %+v", published)
	}
	got, _ = repo.GetByID(ctx, ex.ID)
	if got.DraftVersionID.Valid || !got.PublishedVersionID.Valid || got.PublishedVersionID.UUID != draftID {
		t.Fatalf("pointers after publish: %+v", got)
	}

	// 4. Publish again without a draft -> zero rows (ErrNoRows from :one).
	if _, err = repo.Publish(ctx, ex.ID, itNow); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("publish without draft must yield no-rows, got %v", err)
	}

	// 5. Second draft, publish -> old published becomes unpublished.
	secondID := uuid.Must(uuid.NewV7())
	if _, err = repo.UpsertDraft(ctx, ex.ID, secondID, v, itNow, uuid.NullUUID{}); err != nil {
		t.Fatalf("second draft: %v", err)
	}
	if _, err = repo.Publish(ctx, ex.ID, itNow.Add(2*time.Hour)); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	old, err := repo.GetVersion(ctx, draftID)
	if err != nil || !old.Status.IsUnpublished() {
		t.Fatalf("first version must be unpublished: %+v err=%v", old, err)
	}

	// 6. Rollback to the unpublished version -> new draft with same content.
	rolled, err := repo.CreateDraftFrom(ctx, ex.ID, draftID, uuid.Must(uuid.NewV7()), itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("CreateDraftFrom: %v", err)
	}
	if !rolled.Status.IsDraft() || rolled.AdminNote != "second cut" {
		t.Fatalf("rollback content mismatch: %+v", rolled)
	}

	// 7. Rollback while a draft exists -> zero rows.
	if _, err = repo.CreateDraftFrom(ctx, ex.ID, draftID, uuid.Must(uuid.NewV7()), itNow, uuid.NullUUID{}); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("rollback with existing draft must yield no-rows, got %v", err)
	}

	// 8. Discard -> pointer auto-clears via ON DELETE SET NULL.
	discardedID, err := repo.Discard(ctx, ex.ID)
	if err != nil || discardedID != rolled.ID {
		t.Fatalf("Discard: id=%v err=%v", discardedID, err)
	}
	got, _ = repo.GetByID(ctx, ex.ID)
	if got.DraftVersionID.Valid {
		t.Fatalf("draft pointer must clear on discard: %+v", got)
	}

	// 9. History: three versions total.
	history, err := repo.ListVersions(ctx, ex.ID)
	if err != nil || len(history) != 2 { // published + unpublished (draft deleted)
		t.Fatalf("history: %d err=%v", len(history), err)
	}
}

func TestExerciseCheckpointRestorePreservesCurrentDraft(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "IT checkpoint restore")
	draftID := uuid.Must(uuid.NewV7())
	first := exerciseModel.ExerciseVersion{AdminNote: "first state", Variants: itVariants()}
	first.Variants[0].Topology.Devices = []exerciseModel.Device{{
		ID: uuid.Must(uuid.NewV7()), Name: "db", Type: exerciseModel.DeviceTypeContainer, Image: "postgres",
		EnvVars: []exerciseModel.EnvVar{{Name: "DB_PASS", Value: "encrypted:opaque-ciphertext", Secret: true}},
	}}
	if _, err := repo.UpsertDraft(ctx, ex.ID, draftID, first, itNow, uuid.NullUUID{}); err != nil {
		t.Fatalf("first draft: %v", err)
	}
	checkpointID := uuid.Must(uuid.NewV7())
	checkpoint, err := repo.CreateCheckpoint(ctx, ex.ID, checkpointID, "", itNow.Add(time.Minute), uuid.NullUUID{})
	if err != nil || checkpoint.ID != checkpointID || checkpoint.Status != exerciseModel.VersionStatusCheckpoint {
		t.Fatalf("checkpoint: %+v err=%v", checkpoint, err)
	}
	second := exerciseModel.ExerciseVersion{AdminNote: "second state", Variants: itVariants()}
	if _, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), second, itNow.Add(2*time.Minute), uuid.NullUUID{}); err != nil {
		t.Fatalf("second draft: %v", err)
	}
	beforeRestoreID := uuid.Must(uuid.NewV7())
	restored, err := repo.RestoreVersionPreservingDraft(ctx, ex.ID, checkpointID, beforeRestoreID, itNow.Add(3*time.Minute), uuid.NullUUID{})
	if err != nil || restored.ID != draftID || restored.AdminNote != "first state" {
		t.Fatalf("restored draft: %+v err=%v", restored, err)
	}
	if got := restored.Variants[0].Topology.Devices[0].EnvVars[0].Value; got != "encrypted:opaque-ciphertext" {
		t.Fatalf("restore must copy stored ciphertext without re-encrypting it: %q", got)
	}
	previous, err := repo.GetVersion(ctx, beforeRestoreID)
	if err != nil || previous.Status != exerciseModel.VersionStatusCheckpoint || previous.AdminNote != "second state" {
		t.Fatalf("previous state was not preserved: %+v err=%v", previous, err)
	}
	if _, err := repo.RestoreVersionPreservingDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), itNow, uuid.NullUUID{}); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("missing source must not mutate the draft: %v", err)
	}
	other := mustCreateExercise(t, repo, "IT other checkpoint owner")
	if _, err := repo.UpsertDraft(ctx, other.ID, uuid.Must(uuid.NewV7()), first, itNow, uuid.NullUUID{}); err != nil {
		t.Fatalf("other draft: %v", err)
	}
	foreign, err := repo.CreateCheckpoint(ctx, other.ID, uuid.Must(uuid.NewV7()), "", itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("foreign checkpoint: %v", err)
	}
	if _, err := repo.RestoreVersionPreservingDraft(ctx, ex.ID, foreign.ID, uuid.Must(uuid.NewV7()), itNow, uuid.NullUUID{}); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("foreign source must not mutate the draft: %v", err)
	}
	after, err := repo.GetDraft(ctx, ex.ID)
	if err != nil || after.AdminNote != "first state" {
		t.Fatalf("draft changed after rejected restore: %+v err=%v", after, err)
	}
}

// Snapshot = copy of the working copy: the draft row, or the published
// version while the working copy is not materialized yet. The note is a
// label of the snapshot only: admin_note (content) is copied untouched, and
// restoring the snapshot never carries the label into the working copy.
func TestExerciseCheckpoint_LabelAndPublishedFallback(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "IT checkpoint label")
	content := exerciseModel.ExerciseVersion{AdminNote: "published note", Variants: itVariants()}
	if _, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	published, err := repo.Publish(ctx, ex.ID, itNow)
	if err != nil || published.Label != "" {
		t.Fatalf("published version has no label: %+v err=%v", published, err)
	}

	fromPublished, err := repo.CreateCheckpoint(ctx, ex.ID, uuid.Must(uuid.NewV7()), "", itNow.Add(time.Minute), uuid.NullUUID{})
	if err != nil || !fromPublished.Status.IsCheckpoint() || fromPublished.AdminNote != "published note" || fromPublished.Label != "" || len(fromPublished.Variants) != 1 {
		t.Fatalf("snapshot without a draft row must copy the published content: %+v err=%v", fromPublished, err)
	}

	content.AdminNote = "draft note"
	if _, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	labelled, err := repo.CreateCheckpoint(ctx, ex.ID, uuid.Must(uuid.NewV7()), "Before refactor", itNow.Add(2*time.Minute), uuid.NullUUID{})
	if err != nil || labelled.Label != "Before refactor" || labelled.AdminNote != "draft note" {
		t.Fatalf("draft row wins; label must not touch admin_note: %+v err=%v", labelled, err)
	}
	if listed, err := repo.GetVersion(ctx, labelled.ID); err != nil || listed.Label != "Before refactor" {
		t.Fatalf("label round-trip: %+v err=%v", listed, err)
	}

	content.AdminNote = "later note"
	if _, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	autoID := uuid.Must(uuid.NewV7())
	restored, err := repo.RestoreVersionPreservingDraft(ctx, ex.ID, labelled.ID, autoID, itNow.Add(3*time.Minute), uuid.NullUUID{})
	if err != nil || restored.Label != "" || restored.AdminNote != "draft note" {
		t.Fatalf("restore copies content, never the label: %+v err=%v", restored, err)
	}
	if auto, err := repo.GetVersion(ctx, autoID); err != nil || auto.Label != "" || auto.AdminNote != "later note" {
		t.Fatalf("automatic pre-restore snapshot: %+v err=%v", auto, err)
	}

	empty := mustCreateExercise(t, repo, "IT checkpoint nothing")
	if _, err := repo.CreateCheckpoint(ctx, empty.ID, uuid.Must(uuid.NewV7()), "", itNow, uuid.NullUUID{}); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("nothing to snapshot: %v", err)
	}
}

func TestUpdateExercise_OptimisticLock(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)

	created := mustCreateExercise(t, repo, "IT lock")

	fresh := created
	if err := fresh.UpdateIdentity("IT lock v2", "d", nil, uuid.Nil, itNow.Add(time.Hour)); err != nil {
		t.Fatalf("UpdateIdentity: %v", err)
	}
	affected, err := repo.Update(ctx, fresh, created.UpdatedAt)
	if err != nil || affected != 1 {
		t.Fatalf("first write: affected=%d err=%v", affected, err)
	}

	stale := created
	_ = stale.UpdateIdentity("IT lock v3", "d", nil, uuid.Nil, itNow.Add(2*time.Hour))
	affected, err = repo.Update(ctx, stale, created.UpdatedAt)
	if err != nil || affected != 0 {
		t.Fatalf("stale snapshot must hit 0 rows: affected=%d err=%v", affected, err)
	}
}

// TestListExercisesCursor_KeysetTagsAndSearch drives ListExercisesCursor and
// CountExercises against real SQL — the (created_at, id) tuple comparison,
// the tags && overlap operator, and the ILIKE-over-name-or-description
// search are exactly the class of thing a mocked Querier cannot catch (SQL
// syntax/operator drift, not business logic).
func TestListExercisesCursor_KeysetTagsAndSearch(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)

	// Distinct, increasing created_at values make DESC(created_at, id) order
	// deterministic: d (newest) -> c -> b -> a (oldest).
	a := mustCreateExerciseFull(t, repo, "Nmap Basics", "intro network scanning", []string{"recon", "network"}, itNow)
	b := mustCreateExerciseFull(t, repo, "SQLi Playground", "union based injection lab", []string{"web"}, itNow.Add(1*time.Minute))
	c := mustCreateExerciseFull(t, repo, "Nmap Advanced", "stealth SCANNING techniques", []string{"recon", "advanced"}, itNow.Add(2*time.Minute))
	d := mustCreateExerciseFull(t, repo, "Crypto Basics", "classic cipher primer", []string{"crypto"}, itNow.Add(3*time.Minute))

	// --- cursor-tuple keyset: page size 2, first page then next page ---
	page1, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "", Tags: []string{}, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, LimitVal: 2,
	})
	if err != nil {
		t.Fatalf("ListExercisesCursor page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != d.ID || page1[1].ID != c.ID {
		t.Fatalf("page1 must be [d, c] in DESC(created_at, id) order, got %v", idsOf(page1))
	}
	page2, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "", Tags: []string{}, CursorCreatedAt: page1[1].CreatedAt, CursorID: page1[1].ID, LimitVal: 2,
	})
	if err != nil {
		t.Fatalf("ListExercisesCursor page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != b.ID || page2[1].ID != a.ID {
		t.Fatalf("page2 (cursored past c) must be [b, a], got %v", idsOf(page2))
	}
	page3, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "", Tags: []string{}, CursorCreatedAt: page2[1].CreatedAt, CursorID: page2[1].ID, LimitVal: 2,
	})
	if err != nil || len(page3) != 0 {
		t.Fatalf("page3 (cursored past a) must be empty, got %v err=%v", idsOf(page3), err)
	}

	// --- tags && overlap: "recon" matches a and c, not b or d ---
	tagged, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "", Tags: []string{"recon"}, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, LimitVal: 10,
	})
	if err != nil {
		t.Fatalf("ListExercisesCursor tags=recon: %v", err)
	}
	if len(tagged) != 2 || tagged[0].ID != c.ID || tagged[1].ID != a.ID {
		t.Fatalf("tags && {recon} must match [c, a] only, got %v", idsOf(tagged))
	}
	tagCount, err := db.Queries.CountExercises(ctx, postgres.CountExercisesParams{Search: "", Tags: []string{"recon"}})
	if err != nil || tagCount != 2 {
		t.Fatalf("CountExercises tags=recon: count=%d err=%v", tagCount, err)
	}

	// A tag no exercise has must match nothing.
	noneTagged, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "", Tags: []string{"pwn"}, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, LimitVal: 10,
	})
	if err != nil || len(noneTagged) != 0 {
		t.Fatalf("tags && {pwn} must match nothing, got %v err=%v", idsOf(noneTagged), err)
	}

	// --- ILIKE search over name OR description, case-insensitive ---
	// "scanning" hits a's description and c's (uppercased) description, but
	// neither b nor d.
	searched, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "scanning", Tags: []string{}, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, LimitVal: 10,
	})
	if err != nil {
		t.Fatalf("ListExercisesCursor search=scanning: %v", err)
	}
	if len(searched) != 2 || searched[0].ID != c.ID || searched[1].ID != a.ID {
		t.Fatalf("search=scanning must match [c, a] only (case-insensitive), got %v", idsOf(searched))
	}
	searchCount, err := db.Queries.CountExercises(ctx, postgres.CountExercisesParams{Search: "scanning", Tags: []string{}})
	if err != nil || searchCount != 2 {
		t.Fatalf("CountExercises search=scanning: count=%d err=%v", searchCount, err)
	}

	// A name-only match: "Crypto" hits only d's name.
	byName, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "Crypto", Tags: []string{}, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, LimitVal: 10,
	})
	if err != nil || len(byName) != 1 || byName[0].ID != d.ID {
		t.Fatalf("search=Crypto must match [d] only, got %v err=%v", idsOf(byName), err)
	}

	// --- combined: search AND tags together narrow further ---
	combined, err := listCursor(ctx, db.Queries, postgres.ListExercisesCursorParams{
		Search: "nmap", Tags: []string{"advanced"}, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, LimitVal: 10,
	})
	if err != nil || len(combined) != 1 || combined[0].ID != c.ID {
		t.Fatalf("search=nmap AND tags={advanced} must match [c] only, got %v err=%v", idsOf(combined), err)
	}

	// --- overall count with no filters must be all four ---
	total, err := db.Queries.CountExercises(ctx, postgres.CountExercisesParams{Search: "", Tags: []string{}})
	if err != nil || total != 4 {
		t.Fatalf("CountExercises unfiltered: count=%d err=%v", total, err)
	}
}

func TestListExercisesPage_SortsBeforePagingAndCountsFilteredRows(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	a := mustCreateExerciseFull(t, repo, "Alpha lab", "first", []string{"web"}, itNow)
	_ = mustCreateExerciseFull(t, repo, "Beta lab", "second", []string{"crypto"}, itNow.Add(time.Minute))
	c := mustCreateExerciseFull(t, repo, "Gamma lab", "third", []string{"web"}, itNow.Add(2*time.Minute))

	first, err := listPage(ctx, db.Queries, postgres.ListExercisesPageParams{
		Search: "lab", Tags: []string{}, Status: "", SortBy: "name", SortDir: "asc", LimitVal: 2, OffsetVal: 0,
	})
	if err != nil || len(first) != 2 || first[0].ID != a.ID {
		t.Fatalf("name-ascending first page: ids=%v err=%v", idsOf(first), err)
	}
	second, err := listPage(ctx, db.Queries, postgres.ListExercisesPageParams{
		Search: "lab", Tags: []string{}, Status: "", SortBy: "name", SortDir: "asc", LimitVal: 2, OffsetVal: 2,
	})
	if err != nil || len(second) != 1 || second[0].ID != c.ID {
		t.Fatalf("name-ascending second page: ids=%v err=%v", idsOf(second), err)
	}
	count, err := db.Queries.CountExercisesPage(ctx, postgres.CountExercisesPageParams{Search: "lab", Tags: []string{"web"}, Status: ""})
	if err != nil || count != 2 {
		t.Fatalf("filtered count: count=%d err=%v", count, err)
	}
}

func TestListExercisesPage_FiltersVersionStatus(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	none := mustCreateExercise(t, repo, "No version")
	draft := mustCreateExercise(t, repo, "Draft version")
	published := mustCreateExercise(t, repo, "Published version")

	for _, id := range []uuid.UUID{draft.ID, published.ID} {
		if _, err := repo.UpsertDraft(ctx, id, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{}); err != nil {
			t.Fatalf("create draft for %s: %v", id, err)
		}
	}
	if _, err := repo.Publish(ctx, published.ID, itNow.Add(time.Hour)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for _, tc := range []struct {
		status string
		want   uuid.UUID
	}{
		{status: "none", want: none.ID},
		{status: "draft_only", want: draft.ID},
		{status: "published", want: published.ID},
	} {
		rows, err := listPage(ctx, db.Queries, postgres.ListExercisesPageParams{
			Search: "", Tags: []string{}, Status: tc.status, SortBy: "updated", SortDir: "desc", LimitVal: 25, OffsetVal: 0,
		})
		if err != nil || len(rows) != 1 || rows[0].ID != tc.want {
			t.Errorf("status %q: ids=%v err=%v", tc.status, idsOf(rows), err)
		}
		count, err := db.Queries.CountExercisesPage(ctx, postgres.CountExercisesPageParams{Search: "", Tags: []string{}, Status: tc.status})
		if err != nil || count != 1 {
			t.Errorf("status %q count=%d err=%v", tc.status, count, err)
		}
	}
}

// listCursor runs the raw keyset query and drops the derived status.
func listCursor(ctx context.Context, q *postgres.Queries, arg postgres.ListExercisesCursorParams) ([]postgres.Exercise, error) {
	rows, err := q.ListExercisesCursor(ctx, arg)
	out := make([]postgres.Exercise, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Exercise)
	}
	return out, err
}

// listPage runs the raw offset query and drops the derived status.
func listPage(ctx context.Context, q *postgres.Queries, arg postgres.ListExercisesPageParams) ([]postgres.Exercise, error) {
	rows, err := q.ListExercisesPage(ctx, arg)
	out := make([]postgres.Exercise, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Exercise)
	}
	return out, err
}

func idsOf(rows []postgres.Exercise) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// TestExerciseVersions_RegenFlagsColumnDropped: the regenerate-flags toggle
// never had behavior (flags resolve per team at deploy); the column is gone.
func TestExerciseVersions_RegenFlagsColumnDropped(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	var exists bool
	err := db.Pool.QueryRow(context.Background(), `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		               WHERE table_name = 'exercise_versions' AND column_name = 'regen_flags')`).Scan(&exists)
	if err != nil {
		t.Fatalf("information_schema: %v", err)
	}
	if exists {
		t.Fatal("exercise_versions.regen_flags must be dropped")
	}
}

// TestExerciseDraftDiffersFromPublished: after publish there is no draft row
// (the working copy is the published content); the next save materializes a
// NEW draft row, and jsonb equality decides whether it differs.
func TestExerciseDraftDiffersFromPublished(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "IT has changes")
	content := exerciseModel.ExerciseVersion{AdminNote: "note", Variants: itVariants()}
	firstID := uuid.Must(uuid.NewV7())
	if _, err := repo.UpsertDraft(ctx, ex.ID, firstID, content, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Publish(ctx, ex.ID, itNow); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetDraft(ctx, ex.ID); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("publish must leave no draft row: %v", err)
	}
	if _, err := repo.DraftDiffersFromPublished(ctx, ex.ID); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("no draft row → no comparison row: %v", err)
	}

	secondID := uuid.Must(uuid.NewV7())
	saved, err := repo.UpsertDraft(ctx, ex.ID, secondID, content, itNow.Add(time.Minute), uuid.NullUUID{})
	if err != nil || saved.ID != secondID || saved.ID == firstID {
		t.Fatalf("first save after publish must create a new draft row: %+v err=%v", saved, err)
	}
	if differs, err := repo.DraftDiffersFromPublished(ctx, ex.ID); err != nil || differs {
		t.Fatalf("identical content: differs=%t err=%v", differs, err)
	}

	content.AdminNote = "changed note"
	if _, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if differs, err := repo.DraftDiffersFromPublished(ctx, ex.ID); err != nil || !differs {
		t.Fatalf("admin note change: differs=%t err=%v", differs, err)
	}

	content.AdminNote = "note"
	content.Variants[0].Note = "variant note"
	if _, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if differs, err := repo.DraftDiffersFromPublished(ctx, ex.ID); err != nil || !differs {
		t.Fatalf("variant change: differs=%t err=%v", differs, err)
	}
}

// TestListExerciseUsageEvents: every event with any attachment revision of
// the exercise, once, with archive state evaluated at `now`.
func TestListExerciseUsageEvents(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	events := eventRepo.New(db.Queries)
	ex := mustCreateExercise(t, exercises, "IT used exercise")
	unused := mustCreateExercise(t, exercises, "IT unused exercise")
	draft, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exercises.Publish(ctx, ex.ID, itNow); err != nil {
		t.Fatal(err)
	}
	active := mustCreateEvent(t, events, "usageactive", "Active CTF", itNow.Add(-48*time.Hour), itNow.Add(48*time.Hour), uuid.Nil, itNow)
	archived := mustCreateEvent(t, events, "usagearchived", "Archived CTF", itNow.Add(-48*time.Hour), itNow.Add(-24*time.Hour), uuid.Nil, itNow)
	for revision, eventID := range []uuid.UUID{active.ID, archived.ID, archived.ID} {
		if _, err := db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
			ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: ex.ID, ExerciseVersionID: draft.ID,
			Revision: int32(revision + 1), Status: 1, CreatedAt: itNow,
		}); err != nil {
			t.Fatalf("attach: %v", err)
		}
	}

	usage, err := exercises.ListUsage(ctx, ex.ID, itNow)
	if err != nil || len(usage) != 2 {
		t.Fatalf("usage: %+v err=%v", usage, err)
	}
	if usage[0].ID != active.ID || usage[0].Name != "Active CTF" || usage[0].Archived ||
		usage[1].ID != archived.ID || usage[1].Name != "Archived CTF" || !usage[1].Archived {
		t.Fatalf("usage rows: %+v", usage)
	}
	if none, err := exercises.ListUsage(ctx, unused.ID, itNow); err != nil || len(none) != 0 {
		t.Fatalf("unused exercise: %+v err=%v", none, err)
	}
}
