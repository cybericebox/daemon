package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Uploading the same image again refreshes its row (one row per digest and tag): the tag collector's grace
// starts from the latest upload.
func TestPlatformImageUpsertRefreshesTheUpload(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := imageRegistryRepo.NewPlatformImages(db.Queries)
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	ref, err := imageRegistryModel.NewPlatformRef("web", "sha256:"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	first := uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000b1")
	second := uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000b2")

	if err = repo.Save(ctx, imageRegistryModel.NewPlatformImage(ref, uuid.Nil, first, t0)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err = repo.Save(ctx, imageRegistryModel.NewPlatformImage(ref, uuid.Nil, second, t0.Add(time.Hour))); err != nil {
		t.Fatalf("save again: %v", err)
	}
	var (
		rows       int
		tag, name  string
		by         uuid.UUID
		uploadedAt time.Time
		exercise   uuid.NullUUID
	)
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM platform_images").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}
	err = db.Pool.QueryRow(ctx, "SELECT tag, name, uploaded_by, uploaded_at, exercise_id FROM platform_images WHERE digest = $1", ref.Digest).
		Scan(&tag, &name, &by, &uploadedAt, &exercise)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "web-aaaaaaaaaaaa" || name != "web" || by != second || !uploadedAt.Equal(t0.Add(time.Hour)) || exercise.Valid {
		t.Fatalf("row tag=%s name=%s by=%s at=%s exercise=%v", tag, name, by, uploadedAt, exercise)
	}
}
