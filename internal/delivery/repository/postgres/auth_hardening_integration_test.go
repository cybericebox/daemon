package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Two spellings of an address are one account: the repository normalizes on write and on lookup,
// and the plain unique index on email enforces it.
func TestUserEmailIsNormalizedByTheRepository(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := userRepo.New(db.Queries)
	id := uuid.Must(uuid.NewV7())
	if _, err := repo.Create(ctx, userModel.NewIncompleteUser(id, " Alice@Example.Test ", time.Now())); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{"alice@example.test", "ALICE@EXAMPLE.TEST", " Alice@Example.Test"} {
		got, err := repo.GetByEmail(ctx, spelling)
		if err != nil || got.ID != id || got.Email != "alice@example.test" {
			t.Fatalf("%q: got %v %q, %v", spelling, got.ID, got.Email, err)
		}
	}
	if _, err := repo.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "ALICE@example.test", time.Now())); err == nil {
		t.Fatal("a second spelling of the same address must violate the unique index")
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

// A pending invitation is known by the address the organizer typed, never by the profile name of
// the invited account: searching a name must not find it, and the name column shows the address.
func TestPendingInvitationIsNotSearchableByName(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "invsearch")
	manager := mustSeedUser(t, db, "inv-manager@test.test")
	invited := mustSeedUser(t, db, "secret.person@test.test")
	rtExec(t, db, `UPDATE users SET first_name = 'Zlatan', last_name = 'Ibrahimovic' WHERE id = $1`, invited)
	repo := participantRepo.New(db.Queries)
	if _, _, err := repo.Invite(ctx, event.ID, invited, manager, uuid.NullUUID{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	count := func(search string) int64 {
		n, err := repo.CountMatching(ctx, event.ID, -1, participantRepo.Kind(""), search, nil)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("Zlatan") != 0 || count("Ibrahimovic") != 0 {
		t.Fatal("a pending invitation must not be found by the profile name")
	}
	if count("secret.person@") != 1 {
		t.Fatal("the address the organizer typed must find it")
	}
	rows, err := repo.List(ctx, event.ID, -1, participantRepo.Kind(""), "Zlatan", nil, time.Now().AddDate(1, 0, 0), uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff")), 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("list by name: %d rows, %v", len(rows), err)
	}
	// Once the invitation is accepted the name is the person's to share.
	rtExec(t, db, `UPDATE event_participants SET status = 2 WHERE event_id = $1 AND user_id = $2`, event.ID, invited)
	if count("Zlatan") != 1 {
		t.Fatal("an accepted participant is found by name")
	}
}
