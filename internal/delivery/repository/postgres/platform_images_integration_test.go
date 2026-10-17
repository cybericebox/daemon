package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/mediaRepo"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
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

// The push state of a blob: saved, replaced, read and deleted by digest.
func TestImageBlobPushRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := imageRegistryRepo.NewBlobPushes(db.Queries)
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	d := "sha256:" + strings.Repeat("b", 64)

	if _, found, err := repo.Get(ctx, d); err != nil || found {
		t.Fatalf("empty: found=%v err=%v", found, err)
	}
	p := imageRegistryModel.NewBlobPush(d, t0)
	if err := repo.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Fail(imageRegistryModel.ReasonDigestMismatch, t0.Add(time.Minute))
	if err := repo.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, found, err := repo.Get(ctx, d)
	if err != nil || !found || got.State != imageRegistryModel.BlobFailed || got.Reason != imageRegistryModel.ReasonDigestMismatch || !got.UpdatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("got %+v found=%v err=%v", got, found, err)
	}
	if err = repo.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, found, _ = repo.Get(ctx, d); found {
		t.Fatal("deleted")
	}
}

// The purpose of a chunked upload is stored and read back.
func TestMediaUploadKeepsItsPurpose(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := mediaRepo.New(db.Queries)
	owner := newUser(t, db)
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	up, err := repo.CreateUpload(ctx, mediaModel.NewUpload(uuid.Must(uuid.NewV7()), owner, "sha256-x", "", 10, 4, time.Hour, now, mediaModel.UploadPurposeImageBlob))
	if err != nil || up.Purpose != mediaModel.UploadPurposeImageBlob {
		t.Fatalf("create %+v %v", up, err)
	}
	got, err := repo.GetUpload(ctx, up.ID)
	if err != nil || got.Purpose != mediaModel.UploadPurposeImageBlob {
		t.Fatalf("get %+v %v", got, err)
	}
}
