package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// An address is one account whatever its case: accounts registered before the addresses were
// normalized still hold a mixed-case spelling and must stay reachable.
func TestGetUserByEmailIsCaseInsensitive(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	if _, err := db.Queries.CreateUser(ctx, postgres.CreateUserParams{
		ID: id, Email: "Alice@Example.Test", Role: string(rbac.RoleUser), Status: string(userModel.UserStatusActive),
	}); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{"alice@example.test", "ALICE@EXAMPLE.TEST", "Alice@Example.Test"} {
		got, err := db.Queries.GetUserByEmail(ctx, spelling)
		if err != nil || got.ID != id {
			t.Fatalf("%q: got %v, %v", spelling, got.ID, err)
		}
	}
	if _, err := db.Queries.GetUserByEmail(ctx, "bob@example.test"); err == nil {
		t.Fatal("another address must not match")
	}

	// If two spellings ever coexist, the exact one wins.
	lower := uuid.Must(uuid.NewV7())
	if _, err := db.Queries.CreateUser(ctx, postgres.CreateUserParams{
		ID: lower, Email: "alice@example.test", Role: string(rbac.RoleUser), Status: string(userModel.UserStatusActive),
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Queries.GetUserByEmail(ctx, "alice@example.test"); err != nil || got.ID != lower {
		t.Fatalf("the exact spelling must win: got %v, %v", got.ID, err)
	}
}

// A password reset revokes the account's other reset codes and nobody else's, nor other code types.
func TestDeleteTemporalCodesForUser(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	mine, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	add := func(user uuid.UUID, codeType int32, code string) {
		t.Helper()
		data := []byte(`{"UserID":"` + user.String() + `"}`)
		if _, err := db.Queries.CreateTemporalCode(ctx, postgres.CreateTemporalCodeParams{
			ID: uuid.Must(uuid.NewV7()), Code: code, Type: codeType, Data: data, ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	add(mine, temporalCodeModel.PasswordResettingCodeType, "mine-reset-1")
	add(mine, temporalCodeModel.PasswordResettingCodeType, "mine-reset-2")
	add(mine, temporalCodeModel.EmailChangeCodeType, "mine-email")
	add(other, temporalCodeModel.PasswordResettingCodeType, "other-reset")

	n, err := db.Queries.DeleteTemporalCodesForUser(ctx, postgres.DeleteTemporalCodesForUserParams{Type: temporalCodeModel.PasswordResettingCodeType, UserID: mine.String()})
	if err != nil || n != 2 {
		t.Fatalf("deleted %d, %v; want my two reset codes", n, err)
	}
	for _, code := range []string{"mine-email", "other-reset"} {
		if _, err := db.Queries.GetTemporalCodeByCode(ctx, code); err != nil {
			t.Fatalf("%s must survive: %v", code, err)
		}
	}
	for _, code := range []string{"mine-reset-1", "mine-reset-2"} {
		if _, err := db.Queries.GetTemporalCodeByCode(ctx, code); err == nil {
			t.Fatalf("%s must be gone", code)
		}
	}
}
