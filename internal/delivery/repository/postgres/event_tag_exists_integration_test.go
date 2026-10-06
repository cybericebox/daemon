package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// An event site's origin is allowed while its event exists, archived or not.
func TestEventTagExists_ArchivedCountsDeletedDoesNot(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)
	e, _ := mustSeedEventForConfig(t, db, "tagexists")

	afterArchive := ecNow.Add(365 * 24 * time.Hour)
	if _, err := repo.GetLiveByTag(ctx, "tagexists", afterArchive); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("the archived event must not resolve as live, got %v", err)
	}
	if ok, err := repo.TagExists(ctx, "tagexists"); err != nil || !ok {
		t.Fatalf("an archived event's tag must exist: ok=%v err=%v", ok, err)
	}
	if ok, err := repo.TagExists(ctx, "other"); err != nil || ok {
		t.Fatalf("unknown tag: ok=%v err=%v", ok, err)
	}
	if _, err := repo.Delete(ctx, e.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, err := repo.TagExists(ctx, "tagexists"); err != nil || ok {
		t.Fatalf("a deleted event's tag must not exist: ok=%v err=%v", ok, err)
	}
}
