package postgres_test

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantPreviewRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	preview "github.com/cybericebox/daemon/internal/model/participantPreview"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParticipantPreviewPersistenceScopeAndNullableOverride(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "pvstore")
	user := mustSeedUser(t, db, "preview-storage@test.test")
	other := mustSeedUser(t, db, "preview-storage-other@test.test")
	r := participantPreviewRepo.New(db.Queries)
	if _, err := r.Settings(ctx, event.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("default inherit %v", err)
	}
	if err := r.SaveSettings(ctx, event.ID, []preview.Selection{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	items, err := r.Settings(ctx, event.ID)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("explicit empty %+v %v", items, err)
	}
	name := "Named preview"
	if err = r.SaveConfiguration(ctx, event.ID, nil, true, &name, time.Now()); err != nil {
		t.Fatal(err)
	}
	config, err := r.Configuration(ctx, event.ID)
	if err != nil || !config.Inherit || config.GroupName == nil || *config.GroupName != name {
		t.Fatalf("name-only inheritance %+v %v", config, err)
	}
	if err = r.Inherit(ctx, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Settings(ctx, event.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("reset inherit %v", err)
	}
	selection := uuid.Must(uuid.NewV7())
	run := preview.NewRun(event.ID, user, selection, time.Now())
	run.Flags[uuid.Nil.String()] = "ICE{test}"
	if err = r.Save(ctx, run); err != nil {
		t.Fatal(err)
	}
	loaded, err := r.Get(ctx, event.ID, user, selection)
	if err != nil || loaded.ID != run.ID || loaded.Flags[uuid.Nil.String()] != "ICE{test}" {
		t.Fatalf("load %+v %v", loaded, err)
	}
	if _, err = r.Get(ctx, event.ID, other, selection); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-user read %v", err)
	}
	if _, err = r.Get(ctx, uuid.Must(uuid.NewV7()), user, selection); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-event read %v", err)
	}
	if err = r.Delete(ctx, event.ID, other, selection); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Get(ctx, event.ID, user, selection); err != nil {
		t.Fatal("cross-user deletion")
	}
	replacement := preview.NewRun(event.ID, user, selection, time.Now())
	if err = r.Save(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	loaded, err = r.Get(ctx, event.ID, user, selection)
	if err != nil || loaded.ID != replacement.ID {
		t.Fatal("owned atomic replacement failed")
	}
}

func TestParticipantPreviewMigration172RoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"0172_participant_preview.down.sql", "0172_participant_preview.up.sql"} {
		data, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Pool.Exec(ctx, string(data)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var column bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='exercise_test_deployments' AND column_name='event_id')`).Scan(&column); err != nil || !column {
		t.Fatalf("event scope column %v %v", column, err)
	}
}
func TestParticipantPreviewOwnerLockSerializesTransactions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	first, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	second, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback(ctx)
	key := "preview:owned-lock"
	if _, err = first.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,739221))`, key); err != nil {
		t.Fatal(err)
	}
	var acquired bool
	if err = second.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1::text,739221))`, key).Scan(&acquired); err != nil || acquired {
		t.Fatalf("parallel start lock %v %v", acquired, err)
	}
	if err = first.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = second.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1::text,739221))`, key).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("released lock %v %v", acquired, err)
	}
}

func TestParticipantPreviewExpiredResourceHoldsRemainChargedUntilCleanup(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	owner := mustSeedUser(t, db, "pv-holds@test.test")
	event := uuid.Must(uuid.NewV7())
	now := time.Now()
	ids := []uuid.UUID{}
	for _, via := range []string{"event_preview", "event_preview_group", "pool"} {
		id := uuid.Must(uuid.NewV7())
		ids = append(ids, id)
		scope := uuid.NullUUID{}
		if via != "pool" {
			scope = uuid.NullUUID{UUID: event, Valid: true}
		}
		if err := db.Queries.CreateResourceTestLabHold(ctx, postgres.CreateResourceTestLabHoldParams{ID: id, OwnerID: owner, Via: via, CpuMillicores: 100, MemoryBytes: 1024, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second), EventID: scope, GroupName: "tp-owned"}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Queries.ListActiveResourceTestLabHolds(ctx, now)
	if err != nil || len(rows) != 2 {
		t.Fatalf("expired preview holds not charged: %+v %v", rows, err)
	}
	deleted, err := db.Queries.DeleteExpiredResourceTestLabHolds(ctx, now)
	if err != nil || deleted != 1 {
		t.Fatalf("expiry purge %d %v", deleted, err)
	}
	scoped, err := db.Queries.ListEventPreviewResourceHolds(ctx, uuid.NullUUID{UUID: event, Valid: true})
	if err != nil || len(scoped) != 2 {
		t.Fatalf("scoped holds %+v %v", scoped, err)
	}
	if err = db.Queries.DeleteResourceTestLabHold(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	scoped, err = db.Queries.ListEventPreviewResourceHolds(ctx, uuid.NullUUID{UUID: event, Valid: true})
	if err != nil || len(scoped) != 1 || scoped[0].Via != "event_preview_group" {
		t.Fatalf("cleanup hold %+v %v", scoped, err)
	}
}
