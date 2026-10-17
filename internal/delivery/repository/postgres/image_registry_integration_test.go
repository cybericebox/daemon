package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestImageRegistrySettingsSingletonRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := imageRegistryRepo.New(db.Queries)
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

	if _, err := repo.Get(ctx); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("empty table should be not found, got %v", err)
	}

	first, err := imageRegistryModel.NewSettings("dockerhub", "acme", "acme/platform", t0)
	if err != nil {
		t.Fatal(err)
	}
	first.SetTokens("sealed-push", "sealed-pull", t0)
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Provider != imageRegistryModel.ProviderDockerHub || got.Account != "acme" || got.Repository != "acme/platform" ||
		got.PushTokenCiphertext != "sealed-push" || got.PullTokenCiphertext != "sealed-pull" || !got.UpdatedAt.Equal(t0) {
		t.Fatalf("round trip: %+v", got)
	}

	// A second save replaces the one row.
	t1 := t0.Add(time.Hour)
	second, _ := imageRegistryModel.NewSettings("ghcr", "octo", "octo/tasks", t1)
	second.SetTokens("sealed-push-2", "", t1)
	if err := repo.Save(ctx, second); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err = repo.Get(ctx)
	if err != nil {
		t.Fatalf("get after replace: %v", err)
	}
	if got.Provider != imageRegistryModel.ProviderGHCR || got.Repository != "octo/tasks" ||
		got.PushTokenCiphertext != "sealed-push-2" || got.PullTokenCiphertext != "" || !got.UpdatedAt.Equal(t1) {
		t.Fatalf("replace: %+v", got)
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM image_registry_settings").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("singleton rows=%d err=%v", rows, err)
	}

	if err := repo.Delete(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("deleted settings should be not found, got %v", err)
	}
}
