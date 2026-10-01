package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestUsersQueries_CreateGetRoundTrip exercises the real sqlc queries against
// a disposable PostgreSQL with the actual migrations applied — the class of
// bugs (SQL typos, column drift) that Querier mocks can never catch.
func TestUsersQueries_CreateGetRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()

	id := uuid.Must(uuid.NewV7())
	_, err := db.Queries.CreateUser(ctx, postgres.CreateUserParams{
		ID:             id,
		Email:          "it@test.test",
		FirstName:      "Ivan",
		LastName:       "Test",
		HashedPassword: pgtype.Text{},
		Picture:        "",
		Role:           string(rbac.RoleUser),
		Status:         string(userModel.UserStatusIncomplete),
		EmailConfirmed: false,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := db.Queries.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Email != "it@test.test" || got.Role != string(rbac.RoleUser) {
		t.Fatalf("unexpected row: %+v", got)
	}

	rows, err := db.Queries.ListUsersCursor(ctx, postgres.ListUsersCursorParams{
		Search:          "",
		Roles:           []string{},
		CursorCreatedAt: got.CreatedAt.AddDate(999, 0, 0),
		CursorID:        uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff")),
		LimitVal:        10,
	})
	if err != nil {
		t.Fatalf("ListUsersCursor: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("ListUsersCursor: want the created user, got %+v", rows)
	}
}

func TestUsersPage_SortsAcrossPagesAndCountsFilteredRows(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for i, name := range []string{"Charlie", "Alice", "Bob"} {
		_, err := db.Queries.CreateUser(ctx, postgres.CreateUserParams{
			ID: uuid.Must(uuid.NewV7()), Email: name + "@page.test", FirstName: name,
			Role: string(rbac.RoleUser), Status: string(userModel.UserStatusActive),
			CreatedAt: time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	params := postgres.ListUsersPageParams{
		Search: "@page.test", Roles: []string{}, Status: string(userModel.UserStatusActive),
		SortBy: "name", SortDir: "asc", LimitVal: 2,
	}
	first, err := db.Queries.ListUsersPage(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	params.OffsetVal = 2
	second, err := db.Queries.ListUsersPage(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].FirstName != "Alice" || first[1].FirstName != "Bob" ||
		len(second) != 1 || second[0].FirstName != "Charlie" {
		t.Fatalf("name sort across pages: first=%+v second=%+v", first, second)
	}
	total, err := db.Queries.CountUsers(ctx, postgres.CountUsersParams{Search: "@page.test", Roles: []string{}, Status: string(userModel.UserStatusActive)})
	if err != nil || total != 3 {
		t.Fatalf("filtered total=%d err=%v", total, err)
	}
}

// TestUpdateUser_OptimisticLock exercises the real IS NOT DISTINCT FROM guard:
// a stale expected_updated_at must hit 0 rows, the fresh one must write.
func TestUpdateUser_OptimisticLock(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	users := userRepo.New(db.Queries)

	created, err := users.Create(ctx, userModel.NewIncompleteUser(
		uuid.Must(uuid.NewV7()), "lock@test.test", time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
	))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First writer wins with the loaded snapshot.
	fresh := created
	fresh.UpdateProfile("First", "Writer", time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC))
	affected, err := users.Update(ctx, fresh, created.UpdatedAt)
	if err != nil || affected != 1 {
		t.Fatalf("first write: affected=%d err=%v", affected, err)
	}

	// Second writer still holds the OLD snapshot — must hit 0 rows.
	stale := created
	stale.UpdateProfile("Second", "Writer", time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	affected, err = users.Update(ctx, stale, created.UpdatedAt)
	if err != nil {
		t.Fatalf("stale write err: %v", err)
	}
	if affected != 0 {
		t.Fatalf("stale snapshot must not overwrite (lost update), affected=%d", affected)
	}

	got, err := users.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.FirstName != "First" {
		t.Fatalf("first writer's data must survive, got %q", got.FirstName)
	}
}
