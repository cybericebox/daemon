package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var vpnNow = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func seedVPNUser(t *testing.T, db *testhelpers.TestDB, email string) uuid.UUID {
	t.Helper()
	users := userRepo.New(db.Queries)
	u, err := users.Create(context.Background(), userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, vpnNow))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func upsertVPN(t *testing.T, db *testhelpers.TestDB, userID uuid.UUID, scope string, ref uuid.NullUUID, cfg string) {
	t.Helper()
	if _, err := db.Queries.UpsertUserVPNConfig(context.Background(), postgres.UpsertUserVPNConfigParams{
		ID: uuid.Must(uuid.NewV7()), UserID: userID, Scope: scope, ScopeRef: ref, Config: cfg,
		CreatedAt: vpnNow, UpdatedAt: vpnNow,
	}); err != nil {
		t.Fatalf("upsert(%s): %v", scope, err)
	}
}

// The test scope has a null ref; NULLS NOT DISTINCT makes re-issuing it an upsert
// (one row per user, config replaced) rather than a second row.
func TestUserVPNConfig_TestScopeUpsertsInPlace(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	userID := seedVPNUser(t, db, "vpn-test@test.test")

	upsertVPN(t, db, userID, "test", uuid.NullUUID{}, "cfg-1")
	upsertVPN(t, db, userID, "test", uuid.NullUUID{}, "cfg-2")

	list, err := db.Queries.ListUserVPNConfigs(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("test scope must upsert in place, got %d rows", len(list))
	}

	got, err := db.Queries.GetUserVPNConfig(ctx, postgres.GetUserVPNConfigParams{UserID: userID, Scope: "test", ScopeRef: uuid.NullUUID{}})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Config != "cfg-2" {
		t.Errorf("config = %q, want cfg-2 (latest)", got.Config)
	}
}

// Different scopes (test + per-event) coexist for the same user, and a null-ref
// delete removes only the test row.
func TestUserVPNConfig_ScopesCoexistAndDelete(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	userID := seedVPNUser(t, db, "vpn-scopes@test.test")
	eventA := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	eventB := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}

	upsertVPN(t, db, userID, "test", uuid.NullUUID{}, "test-cfg")
	upsertVPN(t, db, userID, "event", eventA, "event-a-cfg")
	upsertVPN(t, db, userID, "event", eventB, "event-b-cfg")

	list, err := db.Queries.ListUserVPNConfigs(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 coexisting configs, got %d", len(list))
	}

	// The per-event get is keyed by ref.
	gotA, err := db.Queries.GetUserVPNConfig(ctx, postgres.GetUserVPNConfigParams{UserID: userID, Scope: "event", ScopeRef: eventA})
	if err != nil || gotA.Config != "event-a-cfg" {
		t.Fatalf("event A get wrong: %v / %q", err, gotA.Config)
	}

	// Deleting the null-ref test scope leaves the event configs.
	affected, err := db.Queries.DeleteUserVPNConfig(ctx, postgres.DeleteUserVPNConfigParams{UserID: userID, Scope: "test", ScopeRef: uuid.NullUUID{}})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if affected != 1 {
		t.Errorf("delete affected %d, want 1", affected)
	}
	list, _ = db.Queries.ListUserVPNConfigs(ctx, userID)
	if len(list) != 2 {
		t.Errorf("after deleting test scope, want 2 event configs, got %d", len(list))
	}
}
