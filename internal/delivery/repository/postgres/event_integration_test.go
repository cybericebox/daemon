package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Keyset sentinels for a first page — mirrors the exercises/users integration
// harness (and useCase/event's own cursor sentinel), kept local since this
// package tests the SQL directly, without going through the use case.
var (
	evCursorSentinelTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	evMaxUUID            = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
)

var evNow = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func mustCreateEvent(t *testing.T, repo *eventRepo.Repository, tag, name string, availableFrom, archiveAt time.Time, createdBy uuid.UUID, now time.Time) eventModel.Event {
	t.Helper()
	e, err := eventModel.NewEvent(tag, name, availableFrom, archiveAt, createdBy, now)
	if err != nil {
		t.Fatalf("NewEvent(%q): %v", tag, err)
	}
	created, err := repo.Create(context.Background(), e)
	if err != nil {
		t.Fatalf("Create(%q): %v", tag, err)
	}
	return created
}

func TestManagerAssignmentNotificationDefaultIsMigrated(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	var enabled, correctAudience bool
	err := db.Pool.QueryRow(ctx, `SELECT enabled, audience = '{"kind":"signal_subject"}'::jsonb FROM platform_signal_notification_defaults WHERE signal_type = 'event.manager.assigned' AND channel = 'in_app'`).Scan(&enabled, &correctAudience)
	if err != nil || !enabled || !correctAudience {
		t.Fatalf("manager assignment default: enabled=%v correctAudience=%v err=%v", enabled, correctAudience, err)
	}
	var title, body string
	err = db.Pool.QueryRow(ctx, `SELECT title, body FROM notification_in_app_templates WHERE notification_type = 'event.manager.assigned' AND status = 'published' AND scope_event_id IS NULL`).Scan(&title, &body)
	if err != nil || title == "" || body == "" {
		t.Fatalf("manager assignment template: title=%q body=%q err=%v", title, body, err)
	}
}

// TestEventCreateGetRoundTrip drives Create -> GetByID through the real
// eventRepo and asserts every field round-trips: tag, name, the availability
// window, and the created_by/updated_by actor columns. A real, FK-referenced
// user is seeded so the uuid.NullUUID{Valid:true} path is genuinely
// exercised, not just the null one.
func TestEventCreateGetRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	repo := eventRepo.New(db.Queries)

	actor, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "actor@test.test", evNow))
	if err != nil {
		t.Fatalf("seed actor user: %v", err)
	}

	availableFrom := evNow
	archiveAt := evNow.Add(30 * 24 * time.Hour)
	created := mustCreateEvent(t, repo, "roundtrip", "Round Trip Event", availableFrom, archiveAt, actor.ID, evNow)

	if created.Tag != "roundtrip" || created.Name != "Round Trip Event" {
		t.Fatalf("unexpected created row: %+v", created)
	}
	if !created.AvailableFrom.Equal(availableFrom) || !created.ArchiveAt.Equal(archiveAt) {
		t.Fatalf("unexpected window: %+v", created)
	}
	if !created.CreatedAt.Equal(evNow) || !created.UpdatedAt.Equal(evNow) {
		t.Fatalf("unexpected timestamps: %+v", created)
	}
	if !created.CreatedBy.Valid || created.CreatedBy.UUID != actor.ID {
		t.Fatalf("created_by must round-trip the actor: %+v", created.CreatedBy)
	}
	if !created.UpdatedBy.Valid || created.UpdatedBy.UUID != actor.ID {
		t.Fatalf("updated_by must round-trip the actor: %+v", created.UpdatedBy)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.Tag != created.Tag || got.Name != created.Name {
		t.Fatalf("GetByID identity mismatch: got %+v, want %+v", got, created)
	}
	if !got.AvailableFrom.Equal(created.AvailableFrom) || !got.ArchiveAt.Equal(created.ArchiveAt) {
		t.Fatalf("GetByID window mismatch: got %+v, want %+v", got, created)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("GetByID timestamps mismatch: got %+v, want %+v", got, created)
	}
	if got.CreatedBy != created.CreatedBy || got.UpdatedBy != created.UpdatedBy {
		t.Fatalf("GetByID actor columns mismatch: got %+v, want %+v", got, created)
	}
}

func TestOpenEndedEventStartsUnpublishedAndRetainsSeparateNames(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)
	start := evNow.Add(24 * time.Hour)
	created := mustCreateEvent(t, repo, "openended", "Platform label", start, time.Time{}, uuid.Nil, evNow)
	if !created.ArchiveAt.IsZero() || created.Lifecycle.Configured || created.InternalName != "Platform label" || created.Name != "Platform label" {
		t.Fatalf("new open-ended event must be unpublished with two labels: %+v", created)
	}
	if _, err := repo.GetLiveByTag(ctx, created.Tag, start.Add(-time.Second)); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("subdomain must not resolve before platform availability: %v", err)
	}
	if resolved, err := repo.GetLiveByTag(ctx, created.Tag, start); err != nil || resolved.ID != created.ID {
		t.Fatalf("subdomain should resolve for moderators at platform opening: %+v %v", resolved, err)
	}
	count, err := repo.CountOverlappingWithTag(ctx, created.Tag, start.Add(time.Hour), time.Time{}, uuid.Nil)
	if err != nil || count != 1 {
		t.Fatalf("open-ended window must block same-tag reuse: count=%d err=%v", count, err)
	}

	renamed := created
	if err := renamed.UpdatePublicName("Participant label", uuid.Nil, evNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if affected, err := repo.UpdatePublicName(ctx, renamed, created.UpdatedAt); err != nil || affected != 1 {
		t.Fatalf("public rename: affected=%d err=%v", affected, err)
	}
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil || got.InternalName != "Platform label" || got.Name != "Participant label" {
		t.Fatalf("public rename must not change platform label: %+v %v", got, err)
	}
}

func TestEventsPage_SortsAcrossPagesAndFiltersLifecycle(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)
	now := time.Now().UTC().Truncate(time.Second)
	mustCreateEvent(t, repo, "pagecharlie", "Charlie", now.Add(-time.Hour), time.Time{}, uuid.Nil, now)
	mustCreateEvent(t, repo, "pagealice", "Alice", now.Add(-time.Hour), time.Time{}, uuid.Nil, now.Add(time.Second))
	mustCreateEvent(t, repo, "pagebob", "Bob", now.Add(time.Hour), time.Time{}, uuid.Nil, now.Add(2*time.Second))
	params := postgres.ListEventsPageParams{Search: "page", SortBy: "name", SortDir: "asc", Now: now, LimitVal: 2}
	first, err := db.Queries.ListEventsPage(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	params.OffsetVal = 2
	second, err := db.Queries.ListEventsPage(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].InternalName != "Alice" || first[1].InternalName != "Bob" ||
		len(second) != 1 || second[0].InternalName != "Charlie" {
		t.Fatalf("name sort across pages: first=%+v second=%+v", first, second)
	}
	params.OffsetVal = 0
	params.Status = "not_available"
	pending, err := db.Queries.ListEventsPage(ctx, params)
	if err != nil || len(pending) != 1 || pending[0].Tag != "pagebob" {
		t.Fatalf("pending filter=%+v err=%v", pending, err)
	}
	total, err := db.Queries.CountEventsPage(ctx, postgres.CountEventsPageParams{Search: "page", Status: "not_available", Now: now})
	if err != nil || total != 1 {
		t.Fatalf("pending total=%d err=%v", total, err)
	}
}

// TestListEventsCursor_KeysetAndCount drives ListEventsCursor and CountEvents
// against real SQL: newest-first ordering and paging by the (created_at, id)
// tuple is exactly the class of thing a mocked Querier cannot catch.
func TestListEventsCursor_KeysetAndCount(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)

	availableFrom := evNow
	archiveAt := evNow.Add(90 * 24 * time.Hour)

	// Distinct, increasing created_at values make DESC(created_at, id) order
	// deterministic: c (newest) -> b -> a (oldest).
	a := mustCreateEvent(t, repo, "eventa", "Event A", availableFrom, archiveAt, uuid.Nil, evNow)
	b := mustCreateEvent(t, repo, "eventb", "Event B", availableFrom, archiveAt, uuid.Nil, evNow.Add(1*time.Minute))
	c := mustCreateEvent(t, repo, "eventc", "Event C", availableFrom, archiveAt, uuid.Nil, evNow.Add(2*time.Minute))

	page1, err := db.Queries.ListEventsCursor(ctx, postgres.ListEventsCursorParams{
		Search: "", CursorCreatedAt: evCursorSentinelTime, CursorID: evMaxUUID, LimitVal: 2,
	})
	if err != nil {
		t.Fatalf("ListEventsCursor page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != c.ID || page1[1].ID != b.ID {
		t.Fatalf("page1 must be [c, b] in DESC(created_at, id) order, got %v", idsOfEvents(page1))
	}

	page2, err := db.Queries.ListEventsCursor(ctx, postgres.ListEventsCursorParams{
		Search: "", CursorCreatedAt: page1[1].CreatedAt, CursorID: page1[1].ID, LimitVal: 2,
	})
	if err != nil {
		t.Fatalf("ListEventsCursor page2: %v", err)
	}
	if len(page2) != 1 || page2[0].ID != a.ID {
		t.Fatalf("page2 (cursored past b) must be [a], got %v", idsOfEvents(page2))
	}

	page3, err := db.Queries.ListEventsCursor(ctx, postgres.ListEventsCursorParams{
		Search: "", CursorCreatedAt: page2[0].CreatedAt, CursorID: page2[0].ID, LimitVal: 2,
	})
	if err != nil || len(page3) != 0 {
		t.Fatalf("page3 (cursored past a) must be empty, got %v err=%v", idsOfEvents(page3), err)
	}

	total, err := db.Queries.CountEvents(ctx, "")
	if err != nil || total != 3 {
		t.Fatalf("CountEvents: count=%d err=%v", total, err)
	}
}

// TestUpdateEvent_OptimisticLock exercises the real "updated_at IS NOT
// DISTINCT FROM $expected" guard: a stale expected_updated_at must hit 0
// rows (the 409 path), the fresh one must write.
func TestUpdateEvent_OptimisticLock(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)

	availableFrom := evNow
	archiveAt := evNow.Add(30 * 24 * time.Hour)
	created := mustCreateEvent(t, repo, "locktag", "Lock Event", availableFrom, archiveAt, uuid.Nil, evNow)

	// First writer holds the loaded snapshot and wins.
	fresh := created
	if err := fresh.UpdateEvent("locktag", "Lock Event v2", availableFrom, archiveAt, uuid.Nil, evNow.Add(time.Hour)); err != nil {
		t.Fatalf("UpdateEvent (fresh): %v", err)
	}
	affected, err := repo.Update(ctx, fresh, created.UpdatedAt)
	if err != nil || affected != 1 {
		t.Fatalf("first write: affected=%d err=%v", affected, err)
	}

	// Second writer still holds the OLD snapshot — must hit 0 rows.
	stale := created
	if err := stale.UpdateEvent("locktag", "Lock Event v3", availableFrom, archiveAt, uuid.Nil, evNow.Add(2*time.Hour)); err != nil {
		t.Fatalf("UpdateEvent (stale): %v", err)
	}
	affected, err = repo.Update(ctx, stale, created.UpdatedAt)
	if err != nil {
		t.Fatalf("stale write err: %v", err)
	}
	if affected != 0 {
		t.Fatalf("stale snapshot must not overwrite (lost update), affected=%d", affected)
	}
}

func TestArchivePendingEventPreservesHistoryAndWithdrawsTenant(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)
	now := time.Date(2026, 9, 24, 18, 0, 0, 0, time.UTC)
	start := now.Add(24 * time.Hour)
	created := mustCreateEvent(t, repo, "pendingarchive", "Pending", start, start.Add(24*time.Hour), uuid.Nil, now)

	archived := created
	archived.Archive(now, uuid.Nil)
	affected, err := repo.Archive(ctx, archived, created.UpdatedAt)
	if err != nil || affected != 1 {
		t.Fatalf("Archive: affected=%d err=%v", affected, err)
	}
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.AvailableFrom.Equal(start) || !got.ArchiveAt.Equal(now) {
		t.Fatalf("archive must retain original start and close legacy window: %+v", got)
	}
	if got.Lifecycle.FinishAt == nil || !got.Lifecycle.FinishAt.Equal(now) ||
		got.Lifecycle.WithdrawAt == nil || got.Lifecycle.Status(*got.Lifecycle.WithdrawAt) != eventModel.LifecycleWithdrawn {
		t.Fatalf("canonical lifecycle must be withdrawn: %+v", got.Lifecycle)
	}
	if _, err := repo.GetLiveByTag(ctx, created.Tag, now); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("archived tag must no longer resolve to a tenant: %v", err)
	}
}

func TestUpdateEventLifecycle_PersistsPermanentRuntime(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)

	availableFrom := evNow
	archiveAt := evNow.Add(30 * 24 * time.Hour)
	created := mustCreateEvent(t, repo, "runtime", "Runtime Event", availableFrom, archiveAt, uuid.Nil, evNow)
	publishAt := evNow.Add(time.Hour)
	startAt := publishAt.Add(time.Hour)
	lifecycle, err := eventModel.NewLifecycle(eventModel.JoinPolicyRolling, publishAt, startAt, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	updated := created
	updated.UpdateLifecycle(lifecycle, uuid.Nil, evNow.Add(time.Minute))

	affected, err := repo.UpdateLifecycle(ctx, updated, created.UpdatedAt)
	if err != nil || affected != 1 {
		t.Fatalf("UpdateLifecycle: affected=%d err=%v", affected, err)
	}
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Lifecycle.JoinPolicy != lifecycle.JoinPolicy || !got.Lifecycle.PublishAt.Equal(lifecycle.PublishAt) ||
		!got.Lifecycle.StartAt.Equal(lifecycle.StartAt) || got.Lifecycle.FinishAt != nil ||
		got.Lifecycle.WithdrawAt != nil {
		t.Fatalf("canonical lifecycle did not round-trip: %+v", got)
	}
}

// TestDeleteEvent_RemovesRow drives DeleteEvent and asserts the row is
// actually gone via GetByID.
func TestDeleteEvent_RemovesRow(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)

	availableFrom := evNow
	archiveAt := evNow.Add(30 * 24 * time.Hour)
	created := mustCreateEvent(t, repo, "deltag", "Delete Event", availableFrom, archiveAt, uuid.Nil, evNow)

	affected, err := repo.Delete(ctx, created.ID)
	if err != nil || affected != 1 {
		t.Fatalf("Delete: affected=%d err=%v", affected, err)
	}

	if _, err = repo.GetByID(ctx, created.ID); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("GetByID after delete must be not-found, got %v", err)
	}
}

// A tag may be reused only when platform availability windows do not overlap.
func TestEventTagAvailabilityWindows(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)

	availableFrom := evNow
	archiveAt1 := evNow.Add(30 * 24 * time.Hour)
	archiveAt2 := evNow.Add(60 * 24 * time.Hour)

	_ = mustCreateEvent(t, repo, "duptag", "First", availableFrom, archiveAt1, uuid.Nil, evNow)

	dupe, err := eventModel.NewEvent("duptag", "Second", availableFrom, archiveAt1, uuid.Nil, evNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewEvent (dupe): %v", err)
	}
	if _, err = repo.Create(ctx, dupe); err == nil {
		t.Fatal("overlapping tag windows must be rejected")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.ExclusionViolation {
			t.Fatalf("want an exclusion-violation pg error, got %v", err)
		}
	}

	reused, err := eventModel.NewEvent("duptag", "Third", archiveAt1, archiveAt2, uuid.Nil, evNow.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewEvent (reused): %v", err)
	}
	if _, err = repo.Create(ctx, reused); err != nil {
		t.Fatalf("same tag with a disjoint availability window must be allowed: %v", err)
	}
}

func idsOfEvents(rows []postgres.Event) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}
