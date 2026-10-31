package adminAudit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/adminAudit"
)

// The journal against a real Postgres: every filter, and keyset paging that neither skips nor repeats a row.
func TestListAdminActionsFiltersAndPages(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	uc := adminAudit.New(db.Queries)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	actorA, actorB := mustUser(t, db, "audit-a@test.test"), mustUser(t, db, "audit-b@test.test")
	event1 := uuid.Must(uuid.NewV7()).String()
	add := func(i int, actor uuid.UUID, perm, method, route string, status int32, target string) {
		t.Helper()
		if err := uc.RecordAdminAction(ctx, adminAudit.Entry{ActorID: actor, Permission: perm, Method: method, Route: route, ResponseStatus: int(status), Target: target}); err != nil {
			t.Fatal(err)
		}
		// pin the time so the order is the insertion order
		rtExec(t, db, `UPDATE admin_audit_log SET created_at = $1 WHERE id = (SELECT id FROM admin_audit_log ORDER BY created_at DESC, id DESC LIMIT 1)`, t0.Add(time.Duration(i)*time.Minute))
	}
	add(1, actorA, "users.delete", "DELETE", "/users/:userID", 200, "userID:u1")
	add(2, actorA, "users.role.write", "PATCH", "/users/:userID/role", 403, "userID:u2")
	add(3, actorB, "events.write", "POST", "/events/:id/manage/teams", 409, "event:"+event1+" team:t9")
	add(4, actorB, "events.write", "DELETE", "/events/:id/manage/teams/:teamID", 200, "event:"+event1+" team:t10")
	add(5, actorA, "infrastructure.write", "POST", "/infrastructure/agents", 500, "agent:ag1")

	count := func(f adminAudit.Filter) int {
		t.Helper()
		f.Limit = 200
		page, err := uc.ListAdminActions(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Items)
	}
	for name, c := range map[string]struct {
		f    adminAudit.Filter
		want int
	}{
		"all":             {adminAudit.Filter{}, 5},
		"actor":           {adminAudit.Filter{ActorID: uuid.NullUUID{UUID: actorA, Valid: true}}, 3},
		"permission":      {adminAudit.Filter{Permission: "events.write"}, 2},
		"route contains":  {adminAudit.Filter{Route: "/manage/teams"}, 2},
		"route with %":    {adminAudit.Filter{Route: "100%"}, 0},
		"method":          {adminAudit.Filter{Method: "delete"}, 2},
		"status exact":    {adminAudit.Filter{StatusMin: 409, StatusMax: 409}, 1},
		"status 2xx":      {adminAudit.Filter{StatusMin: 200, StatusMax: 299}, 2},
		"status 4xx":      {adminAudit.Filter{StatusMin: 400, StatusMax: 499}, 2},
		"status 5xx":      {adminAudit.Filter{StatusMin: 500, StatusMax: 599}, 1},
		"kind event":      {adminAudit.Filter{TargetKind: "event"}, 2},
		"kind team":       {adminAudit.Filter{TargetKind: "team"}, 2},
		"kind vent (not)": {adminAudit.Filter{TargetKind: "vent"}, 0},
		"kind userID":     {adminAudit.Filter{TargetKind: "userID"}, 2},
		"target id":       {adminAudit.Filter{TargetID: event1}, 2},
		"kind and id":     {adminAudit.Filter{TargetKind: "agent", TargetID: "ag1"}, 1},
	} {
		if got := count(c.f); got != c.want {
			t.Errorf("%s: %d rows, want %d", name, got, c.want)
		}
	}
	from, to := t0.Add(2*time.Minute), t0.Add(4*time.Minute)
	if got := count(adminAudit.Filter{From: &from, To: &to}); got != 3 {
		t.Errorf("from/to inclusive: %d rows, want 3", got)
	}

	// keyset paging: pages of 2 walk all 5 rows once, newest first, with no cursor on the last page
	var seen []time.Time
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		page, err := uc.ListAdminActions(ctx, adminAudit.Filter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			seen = append(seen, row.CreatedAt)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("paging returned %d rows, want 5", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if !seen[i].Before(seen[i-1]) {
			t.Fatalf("not newest-first without repeats: %v", seen)
		}
	}
	if _, err := uc.ListAdminActions(ctx, adminAudit.Filter{Cursor: "%%%"}); err != adminAudit.ErrInvalidCursor {
		t.Fatalf("a garbage cursor: %v", err)
	}

	// the found count follows the filters and ignores paging
	for name, c := range map[string]struct {
		f    adminAudit.Filter
		want int64
	}{
		"all":           {adminAudit.Filter{}, 5},
		"paged ignored": {adminAudit.Filter{Limit: 2, Cursor: seenCursor(t, uc)}, 5},
		"actor":         {adminAudit.Filter{ActorID: uuid.NullUUID{UUID: actorA, Valid: true}}, 3},
		"4xx":           {adminAudit.Filter{StatusMin: 400, StatusMax: 499}, 2},
		"none":          {adminAudit.Filter{TargetKind: "nope"}, 0},
	} {
		n, err := uc.CountAdminActions(ctx, c.f)
		if err != nil || n != c.want {
			t.Errorf("count %s: %d %v, want %d", name, n, err, c.want)
		}
	}

	// the export walks every matching row once, newest first, and honours the cap flag
	var exported int
	truncated, err := uc.ExportAdminActions(
		ctx,
		adminAudit.Filter{Limit: 1, Cursor: "ignored"},
		func(postgres.AdminAuditLog) error { exported++; return nil },
	)
	if err != nil || truncated || exported != 5 {
		t.Fatalf("export: %d rows truncated=%v err=%v", exported, truncated, err)
	}
}

func seenCursor(t *testing.T, uc *adminAudit.UseCase) string {
	t.Helper()
	page, err := uc.ListAdminActions(context.Background(), adminAudit.Filter{Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page: %v %q", err, page.NextCursor)
	}
	return page.NextCursor
}

// What an action changed is stored as "before -> after" per field, and a secret field never reaches the
// database: the row holds a redaction marker only.
func TestRecordedChangesNeverStoreASecret(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	uc := adminAudit.New(db.Queries)
	actor := mustUser(t, db, "audit-changes@test.test")

	type form struct {
		Role         string
		SMTPPassword string
		Backup       struct{ RecoveryCode, Window string }
	}
	before := form{Role: "user", SMTPPassword: "hunter2-old"}
	after := form{Role: "admin", SMTPPassword: "hunter2-new"}
	before.Backup.RecoveryCode, after.Backup.RecoveryCode = "111-222", "333-444"
	before.Backup.Window, after.Backup.Window = "1h", "2h"
	if err := uc.RecordAdminAction(ctx, adminAudit.Entry{
		ActorID:        actor,
		Permission:     "settings.write",
		Method:         "PUT",
		Route:          "/settings/:key",
		ResponseStatus: 200,
		Changes:        adminAudit.Diff(before, after, "Role", "Backup.Window", "SMTPPassword", "Backup.RecoveryCode"),
	}); err != nil {
		t.Fatal(err)
	}
	// A hand-built change that names a secret is cleaned at the write too.
	if err := uc.RecordAdminAction(ctx, adminAudit.Entry{
		ActorID:        actor,
		Permission:     "settings.write",
		Method:         "PUT",
		Route:          "/settings/:key",
		ResponseStatus: 200,
		Changes: []adminAudit.Change{
			{Field: "ApiToken", Before: "tok-old", After: "tok-new"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := db.Pool.QueryRow(ctx, `SELECT string_agg(changes::text, ' ') FROM admin_audit_log`).
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"hunter2", "111-222", "333-444", "tok-old", "tok-new"} {
		if strings.Contains(stored, leaked) {
			t.Fatalf("%q reached the database: %s", leaked, stored)
		}
	}
	for _, want := range []string{`"Role"`, `"admin"`, `"Backup.Window"`, `"2h"`, `"Redacted": true`} {
		if !strings.Contains(stored, want) {
			t.Errorf("%s missing from the stored changes: %s", want, stored)
		}
	}
	page, err := uc.ListAdminActions(
		ctx,
		adminAudit.Filter{ActorID: uuid.NullUUID{UUID: actor, Valid: true}},
	)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("list: %d %v", len(page.Items), err)
	}
}

func rtExec(t *testing.T, db *testhelpers.TestDB, sql string, args ...any) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func mustUser(t *testing.T, db *testhelpers.TestDB, email string) uuid.UUID {
	t.Helper()
	u, err := userRepo.New(db.Queries).Create(context.Background(), userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
