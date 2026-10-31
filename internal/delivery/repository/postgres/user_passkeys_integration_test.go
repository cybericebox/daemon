package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/passkeyRepo"
	passkeyModel "github.com/cybericebox/daemon/internal/model/passkey"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func newPasskey(user uuid.UUID, n int) passkeyModel.Passkey {
	return passkeyModel.Passkey{
		ID: uuid.Must(uuid.NewV7()), UserID: user, CredentialID: []byte(fmt.Sprintf("cred-%d-%s", n, user)),
		PublicKey: []byte("pk"), Name: "Key", UserVerified: true, CreatedAt: time.Now(),
	}
}

// The cap, the uniqueness of a credential, the forward-only counter and the guarded rename/delete are SQL rules.
func TestUserPasskeys_SQLRules(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	alice := mustSeedUser(t, db, "alice-pk@test.test")
	bob := mustSeedUser(t, db, "bob-pk@test.test")
	repo := passkeyRepo.New(db.Queries)

	first := newPasskey(alice, 0)
	for i := range 3 {
		p := first
		if i > 0 {
			p = newPasskey(alice, i)
		}
		if _, ok, err := repo.Create(ctx, p, 3); err != nil || !ok {
			t.Fatalf("create %d: %v %v", i, ok, err)
		}
	}
	if _, ok, err := repo.Create(ctx, newPasskey(alice, 9), 3); err != nil || ok {
		t.Fatalf("over the cap must be refused without an error, got %v %v", ok, err)
	}
	if _, ok, err := repo.Create(ctx, newPasskey(bob, 0), 3); err != nil || !ok {
		t.Fatalf("the cap is per person: %v %v", ok, err)
	}
	dup := newPasskey(bob, 1)
	dup.CredentialID = first.CredentialID
	if _, _, err := repo.Create(ctx, dup, 3); err == nil {
		t.Fatal("a credential id is unique across people")
	}

	now := time.Now()
	for _, c := range []struct {
		count uint32
		want  bool
	}{{0, true}, {5, true}, {5, false}, {3, false}, {6, true}} {
		if got, err := repo.RecordUse(ctx, first.ID, c.count, false, now); err != nil || got != c.want {
			t.Fatalf("counter %d: want %v, got %v (%v)", c.count, c.want, got, err)
		}
	}
	// An authenticator without a counter stays at zero.
	zero := newPasskey(bob, 2)
	if _, _, err := repo.Create(ctx, zero, 3); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.RecordUse(ctx, zero.ID, 0, false, now); err != nil || !got {
		t.Fatalf("zero counter on a zero key: %v %v", got, err)
	}

	if ok, _ := repo.Rename(ctx, bob, first.ID, "x"); ok {
		t.Fatal("another person's key cannot be renamed")
	}
	if ok, _ := repo.Delete(ctx, bob, first.ID); ok {
		t.Fatal("another person's key cannot be deleted")
	}
	if ok, err := repo.Delete(ctx, alice, first.ID); err != nil || !ok {
		t.Fatalf("own key: %v %v", ok, err)
	}
	if n, _ := db.Queries.CountUserLoginMethods(ctx, alice); n != 2 {
		t.Fatalf("login methods count the passkeys, got %d", n)
	}
}
