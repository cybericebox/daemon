package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestEventRegistrySecretsAreScopedByEvent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := imageRegistryRepo.NewEventSecrets(db.Queries)
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	evA := mustCreateSubscriptionEvent(t, db, "regseca")
	evB := mustCreateSubscriptionEvent(t, db, "regsecb")

	mk := func(ev uuid.UUID, host, prefix string) imageRegistryModel.EventSecret {
		s, err := imageRegistryModel.NewEventSecret(ev.String(), host, prefix, "bob", t0)
		if err != nil {
			t.Fatal(err)
		}
		s.SetToken("sealed-"+host+prefix, t0)
		return s
	}
	a := mk(evA, "ghcr.io", "acme")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Create(ctx, mk(evA, "ghcr.io", "")); err != nil {
		t.Fatalf("create host-only: %v", err)
	}
	// The same host and prefix in another event is fine; in the same event it is a unique violation.
	if err := repo.Create(ctx, mk(evB, "ghcr.io", "acme")); err != nil {
		t.Fatalf("same pair in another event: %v", err)
	}
	err := repo.Create(ctx, mk(evA, "ghcr.io", "acme"))
	if _, ok := repositoryTools.UniqueViolationError(err, imageRegistryModel.ErrDuplicateEventSecret); !ok {
		t.Fatalf("duplicate should be a unique violation, got %v", err)
	}

	list, err := repo.List(ctx, evA)
	if err != nil || len(list) != 2 || list[0].RepositoryPrefix != "" || list[1].RepositoryPrefix != "acme" {
		t.Fatalf("list A: %+v %v", list, err)
	}
	got, err := repo.Get(ctx, evA, a.ID)
	if err != nil || got.TokenCiphertext != "sealed-ghcr.ioacme" || got.Username != "bob" || !got.CreatedAt.Equal(t0) {
		t.Fatalf("get: %+v %v", got, err)
	}

	// Event B cannot see, change or delete event A's secret.
	if _, err = repo.Get(ctx, evB, a.ID); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("cross-event get: %v", err)
	}
	moved := a
	moved.EventID = evB
	moved.Username = "mallory"
	if found, uerr := repo.Update(ctx, moved); uerr != nil || found {
		t.Fatalf("cross-event update: %v %v", found, uerr)
	}
	if found, derr := repo.Delete(ctx, evB, a.ID); derr != nil || found {
		t.Fatalf("cross-event delete: %v %v", found, derr)
	}

	t1 := t0.Add(time.Hour)
	if err = a.Edit("quay.io", "team", "alice", t1); err != nil {
		t.Fatal(err)
	}
	a.SetToken("sealed-2", t1)
	if found, uerr := repo.Update(ctx, a); uerr != nil || !found {
		t.Fatalf("update: %v %v", found, uerr)
	}
	got, _ = repo.Get(ctx, evA, a.ID)
	if got.Host != "quay.io" || got.RepositoryPrefix != "team" || got.Username != "alice" || got.TokenCiphertext != "sealed-2" || !got.UpdatedAt.Equal(t1) || !got.CreatedAt.Equal(t0) {
		t.Fatalf("after update: %+v", got)
	}

	if found, derr := repo.Delete(ctx, evA, a.ID); derr != nil || !found {
		t.Fatalf("delete: %v %v", found, derr)
	}
	// Deleting an event removes its secrets.
	if _, err = db.Pool.Exec(ctx, "DELETE FROM events WHERE id = $1", evB); err == nil {
		if rest, _ := repo.List(ctx, evB); len(rest) != 0 {
			t.Fatalf("secrets outlive the event: %+v", rest)
		}
	}
}
