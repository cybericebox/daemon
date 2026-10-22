package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventContentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/liveScreenRepo"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestEventLiveLayoutDraftPublishRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, _ := mustSeedEventForConfig(t, db, "liveroundtrip")
	repo := eventContentRepo.New(db.Queries)
	screens := liveScreenRepo.New(db.Queries)
	now := time.Now().UTC()

	settings, err := repo.GetSettings(ctx, event.ID)
	if err != nil {
		t.Fatalf("read migrated live layout: %v", err)
	}
	if settings.Live.Version != 1 || settings.LiveDraft != nil {
		t.Fatalf("unexpected initial live state: version=%d draft=%v", settings.Live.Version, settings.LiveDraft)
	}
	first, err := screens.FirstDisplay(ctx, event.ID)
	if err != nil || first.Name != "Екран 1" || first.Position != 0 {
		t.Fatalf("a new event has «Екран 1»: %+v, %v", first, err)
	}

	draft := eventContentModel.DefaultLiveLayout()
	draft.Theme = "light"
	affected, err := screens.SaveDisplayDraft(ctx, event.ID, first.ID, draft, now)
	if err != nil || affected != 1 {
		t.Fatalf("save live draft: affected=%d err=%v", affected, err)
	}
	settings, err = repo.GetSettings(ctx, event.ID)
	if err != nil {
		t.Fatalf("read live draft: %v", err)
	}
	if settings.Live.Theme != "dark" || settings.LiveDraft == nil || settings.LiveDraft.Theme != "light" {
		t.Fatalf("draft changed published layout or did not persist: published=%q draft=%+v", settings.Live.Theme, settings.LiveDraft)
	}

	published, err := screens.PublishDisplay(ctx, event.ID, first.ID, now)
	if err != nil {
		t.Fatalf("publish live draft: %v", err)
	}
	if published.Version != 2 || published.Theme != "light" {
		t.Fatalf("unexpected published live layout: version=%d theme=%q", published.Version, published.Theme)
	}
	settings, err = repo.GetSettings(ctx, event.ID)
	if err != nil {
		t.Fatalf("read published live layout: %v", err)
	}
	if settings.Live.Version != 2 || settings.Live.Theme != "light" || settings.LiveDraft != nil {
		t.Fatalf("published state did not persist: version=%d theme=%q draft=%v", settings.Live.Version, settings.Live.Theme, settings.LiveDraft)
	}
	if _, err = screens.PublishDisplay(ctx, event.ID, first.ID, now); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("publishing without a draft should find no row, got %v", err)
	}

	// Each screen keeps its own layout, draft and version.
	second, err := screens.CreateDisplay(ctx, eventContentModel.LiveDisplay{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, Name: "Фойє", Layout: eventContentModel.DefaultLiveLayout(), CreatedAt: now})
	if err != nil || second.Position != 1 || second.Layout.Version != 1 {
		t.Fatalf("second screen = %+v, %v", second, err)
	}
	if _, err = screens.SaveDisplayDraft(ctx, event.ID, second.ID, draft, now); err != nil {
		t.Fatal(err)
	}
	if again, _ := screens.FirstDisplay(ctx, event.ID); again.ID != first.ID || again.Draft != nil || again.Layout.Version != 2 {
		t.Fatalf("the second screen's draft touched the first: %+v", again)
	}
	if affected, err := screens.SaveDisplayDraft(ctx, event.ID, uuid.Must(uuid.NewV7()), draft, now); err != nil || affected != 0 {
		t.Fatalf("an unknown screen was written: %d, %v", affected, err)
	}
	other, _ := mustSeedEventForConfig(t, db, "liveroundtripother")
	if affected, _ := screens.SaveDisplayDraft(ctx, other.ID, second.ID, draft, now); affected != 0 {
		t.Fatal("a screen was written through another event")
	}
}

func runMigrationScript(t *testing.T, db *testhelpers.TestDB, name string) {
	t.Helper()
	script, err := os.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(context.Background(), string(script)); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// The single layout, draft and link of an event become its screen
// «Екран 1» unchanged; running the migration again changes nothing.
func TestEventLiveScreensMigrationKeepsLayoutDraftAndLink(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, actorID := mustSeedEventForConfig(t, db, "livescreensmigration")
	plain, _ := mustSeedEventForConfig(t, db, "livescreensmigrationplain")
	now := time.Now().UTC().Truncate(time.Microsecond)

	// Back to the schema before screens existed.
	runMigrationScript(t, db, "0193_event_live_screens.down.sql")

	published := eventContentModel.DefaultLiveLayout()
	published.Version, published.Theme = 7, "light"
	published.Scenes = map[string]eventContentModel.LiveScene{"finished": {Grid: eventContentModel.LiveGrid{Cols: 4, Rows: 4},
		Widgets: []eventContentModel.LiveWidget{{ID: "podium", Type: "podium", X: 1, Y: 1, W: 4, H: 2, Props: map[string]json.RawMessage{"variant": json.RawMessage(`"cards"`)}}}}}
	draft := eventContentModel.DefaultLiveLayout()
	draft.Version, draft.Aspect = 7, "4:3"
	publishedJSON, _ := json.Marshal(published)
	draftJSON, _ := json.Marshal(draft)
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET live_layout=$2, live_layout_draft=$3 WHERE id=$1`, event.ID, publishedJSON, draftJSON); err != nil {
		t.Fatal(err)
	}
	link, _, _ := eventContentModel.NewLiveScreenLink(event.ID, uuid.Nil, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	revoked, _, _ := eventContentModel.NewLiveScreenLink(event.ID, uuid.Nil, actorID, now.Add(-time.Hour), eventContentModel.LiveScreenExpiryNone, nil)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO event_live_screen_links (id, event_id, token_hash, created_at, created_by, revoked_at) VALUES ($1, $2, $3, $4, $5, NULL), ($6, $2, $7, $8, $5, $4)`,
		link.ID, event.ID, link.TokenHash, now, actorID, revoked.ID, revoked.TokenHash, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	var defaultLayout []byte
	if err := db.Pool.QueryRow(ctx, `SELECT live_layout FROM events WHERE id=$1`, plain.ID).Scan(&defaultLayout); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		runMigrationScript(t, db, "0193_event_live_screens.up.sql")
		screens := liveScreenRepo.New(db.Queries)
		displays, err := screens.ListDisplays(ctx, event.ID)
		if err != nil || len(displays) != 1 {
			t.Fatalf("run %d: screens = %+v, %v", run, displays, err)
		}
		first := displays[0]
		if first.Name != "Екран 1" || first.Position != 0 || first.Draft == nil {
			t.Fatalf("run %d: first screen = %+v", run, first)
		}
		var sameLayout, sameDraft bool
		if err = db.Pool.QueryRow(ctx, `SELECT layout = $2::jsonb, layout_draft = $3::jsonb FROM event_live_screens WHERE id=$1`, first.ID, publishedJSON, draftJSON).Scan(&sameLayout, &sameDraft); err != nil || !sameLayout || !sameDraft {
			t.Fatalf("run %d: layout kept=%v draft kept=%v err=%v", run, sameLayout, sameDraft, err)
		}
		if first.Layout.Version != 7 || first.Layout.Scenes["finished"].Widgets[0].Type != "podium" || first.Draft.Aspect != "4:3" {
			t.Fatalf("run %d: layout = %+v draft = %+v", run, first.Layout, first.Draft)
		}
		// Both links (working and revoked) belong to «Екран 1»; the working one still opens it.
		active, err := screens.Active(ctx, event.ID, first.ID, now)
		if err != nil || active.ID != link.ID || active.ScreenID != first.ID || !bytes.Equal(active.TokenHash, link.TokenHash) {
			t.Fatalf("run %d: active link = %+v, %v", run, active, err)
		}
		old, err := screens.GetByTokenHash(ctx, revoked.TokenHash)
		if err != nil || old.ScreenID != first.ID || old.RevokedAt == nil {
			t.Fatalf("run %d: revoked link = %+v, %v", run, old, err)
		}
		// An event that never changed its layout keeps the default one.
		plainScreen, err := screens.FirstDisplay(ctx, plain.ID)
		var same bool
		if err != nil || db.Pool.QueryRow(ctx, `SELECT layout = $2::jsonb AND layout_draft IS NULL FROM event_live_screens WHERE id=$1`, plainScreen.ID, defaultLayout).Scan(&same) != nil || !same {
			t.Fatalf("run %d: the default layout changed: %+v, %v", run, plainScreen, err)
		}
	}
	var columns int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='events' AND column_name IN ('live_layout','live_layout_draft')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("the event layout columns must be gone: %d, %v", columns, err)
	}
	// A new event gets its first screen from the trigger.
	created, _ := mustSeedEventForConfig(t, db, "livescreensmigrationnew")
	if first, err := liveScreenRepo.New(db.Queries).FirstDisplay(ctx, created.ID); err != nil || first.Name != "Екран 1" || first.Layout.Version != 1 {
		t.Fatalf("new event screen = %+v, %v", first, err)
	}
}

// Down keeps the first screen's layout and draft on the event and the first
// screen's links; the other screens go.
func TestEventLiveScreensMigrationDownKeepsTheFirstScreen(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, actorID := mustSeedEventForConfig(t, db, "livescreensdown")
	screens := liveScreenRepo.New(db.Queries)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first, err := screens.FirstDisplay(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft := eventContentModel.DefaultLiveLayout()
	draft.Theme = "light"
	if _, err = screens.SaveDisplayDraft(ctx, event.ID, first.ID, draft, now); err != nil {
		t.Fatal(err)
	}
	second, err := screens.CreateDisplay(ctx, eventContentModel.LiveDisplay{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, Name: "Фойє", Layout: eventContentModel.DefaultLiveLayout(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := eventContentModel.NewLiveScreenLink(event.ID, first.ID, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	b, _, _ := eventContentModel.NewLiveScreenLink(event.ID, second.ID, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	if err = screens.Issue(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = screens.Issue(ctx, b); err != nil {
		t.Fatal(err)
	}
	runMigrationScript(t, db, "0193_event_live_screens.down.sql")
	var theme string
	var links int
	if err = db.Pool.QueryRow(ctx, `SELECT live_layout_draft->>'theme' FROM events WHERE id=$1`, event.ID).Scan(&theme); err != nil || theme != "light" {
		t.Fatalf("draft theme = %q, %v", theme, err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_live_screen_links WHERE event_id=$1 AND revoked_at IS NULL AND id=$2`, event.ID, a.ID).Scan(&links); err != nil || links != 1 {
		t.Fatalf("the first screen's link must stay: %d, %v", links, err)
	}
	runMigrationScript(t, db, "0193_event_live_screens.up.sql")
	if active, err := screens.Active(ctx, event.ID, mustFirstScreen(t, db, event.ID), now); err != nil || active.ID != a.ID {
		t.Fatalf("link after down/up = %+v, %v", active, err)
	}
}

func mustFirstScreen(t *testing.T, db *testhelpers.TestDB, eventID uuid.UUID) uuid.UUID {
	t.Helper()
	first, err := liveScreenRepo.New(db.Queries).FirstDisplay(context.Background(), eventID)
	if err != nil {
		t.Fatal(err)
	}
	return first.ID
}
