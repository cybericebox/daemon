package media_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

func archiveRow(id, owner uuid.UUID, size int64, received int32) postgres.MediaUpload {
	r := row(id, owner, size, received)
	r.Purpose = string(mediaModel.UploadPurposeExerciseArchive)
	return r
}

// A task archive may carry images: its upload may be as large as the archive limit plus one image archive.
func TestStartUpload_ExerciseArchivePurposeAllowsImages(t *testing.T) {
	uc, q, _ := uploadUC(t)
	owner := uuid.Must(uuid.NewV7())
	limit := exerciseModel.DefaultArchiveLimits().WithImagesBytes()
	q.EXPECT().CreateMediaUpload(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateMediaUploadParams) (postgres.MediaUpload, error) {
		if p.SizeBytes != limit || p.Purpose != "exercise-archive" {
			t.Fatalf("params %+v", p)
		}
		return postgres.MediaUpload{ID: p.ID, CreatedBy: owner, SizeBytes: p.SizeBytes, ChunkBytes: p.ChunkBytes, ExpiresAt: p.ExpiresAt}, nil
	})
	if _, err := uc.StartUpload(context.Background(), "tasks.zip", "", limit, owner, mediaModel.UploadPurposeExerciseArchive); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if _, err := uc.StartUpload(context.Background(), "tasks.zip", "", limit+1, owner, mediaModel.UploadPurposeExerciseArchive); !errors.Is(err, mediaModel.ErrFileTooLarge.Err()) {
		t.Fatalf("over the limit: %v", err)
	}
	if limit != 512<<20+10<<30 {
		t.Fatalf("limit %d", limit)
	}
}

// The archive limit saved in the platform settings sets the task archive upload limit on the next upload.
func TestStartUpload_ExerciseArchiveFollowsTheSavedLimit(t *testing.T) {
	uc, q, _ := uploadUC(t)
	owner := uuid.Must(uuid.NewV7())
	limit := int64(2 << 30)
	uc.SetArchiveUploadLimit(func(context.Context) int64 { return limit })
	q.EXPECT().CreateMediaUpload(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateMediaUploadParams) (postgres.MediaUpload, error) {
		return postgres.MediaUpload{ID: p.ID, CreatedBy: owner, SizeBytes: p.SizeBytes, ChunkBytes: p.ChunkBytes, ExpiresAt: p.ExpiresAt}, nil
	})
	if _, err := uc.StartUpload(context.Background(), "tasks.zip", "", limit, owner, mediaModel.UploadPurposeExerciseArchive); err != nil {
		t.Fatalf("at the saved limit: %v", err)
	}
	if _, err := uc.StartUpload(context.Background(), "tasks.zip", "", limit+1, owner, mediaModel.UploadPurposeExerciseArchive); !errors.Is(err, mediaModel.ErrFileTooLarge.Err()) {
		t.Fatalf("over the saved limit: %v", err)
	}
}

// A task archive upload is read where it lies, never assembled into a file.
func TestCompleteUpload_ExerciseArchiveIsNotAFile(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	payload := []byte("0123456789")
	stored(st, id, payload)
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(archiveRow(id, owner, 10, 3), nil)
	sum := sha256.Sum256(payload)
	if _, err := uc.CompleteUpload(context.Background(), id, owner, hex.EncodeToString(sum[:])); !errors.Is(err, mediaModel.ErrUploadNotAFile.Err()) {
		t.Fatalf("%v", err)
	}
	if len(st.objects) != 3 || len(st.copies) != 0 {
		t.Fatalf("nothing assembled: %d objects, %d copies", len(st.objects), len(st.copies))
	}
}

func readAt(t *testing.T, r io.ReaderAt, off int64, n int) (string, error) {
	t.Helper()
	p := make([]byte, n)
	got, err := r.ReadAt(p, off)
	return string(p[:got]), err
}

// OpenUploadAt reads any range of the upload across its chunk objects; sequential small reads fetch each chunk
// once (read-ahead), past the end is io.EOF.
func TestOpenUploadAt_ReadsRangesAcrossChunks(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	stored(st, id, []byte("0123456789"))
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(archiveRow(id, owner, 10, 3), nil)
	r, up, err := uc.OpenUploadAt(context.Background(), id)
	if err != nil || up.SizeBytes != 10 {
		t.Fatalf("upload %+v err %v", up, err)
	}
	for _, c := range []struct {
		off  int64
		n    int
		want string
		eof  bool
	}{{0, 10, "0123456789", false}, {3, 3, "345", false}, {7, 5, "789", true}, {10, 1, "", true}} {
		got, err := readAt(t, r, c.off, c.n)
		if got != c.want || (c.eof != errors.Is(err, io.EOF)) || (!c.eof && err != nil) {
			t.Fatalf("ReadAt(%d, %d) = %q, %v", c.off, c.n, got, err)
		}
	}
	// Sequential one-byte reads: one range request per chunk object.
	r, _, _ = func() (io.ReaderAt, mediaModel.Upload, error) {
		q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(archiveRow(id, owner, 10, 3), nil)
		return uc.OpenUploadAt(context.Background(), id)
	}()
	st.ranges = 0
	var all []byte
	for off := int64(0); off < 10; off++ {
		b, err := readAt(t, r, off, 1)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	if string(all) != "0123456789" || st.ranges != 3 {
		t.Fatalf("read %q with %d range requests", all, st.ranges)
	}
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(postgres.MediaUpload{}, pgx.ErrNoRows)
	if _, _, err = uc.OpenUploadAt(context.Background(), id); !errors.Is(err, mediaModel.ErrUploadNotFound.Err()) {
		t.Fatalf("absent: %v", err)
	}
}

// OpenFileAt reads ranges of a stored file's blob.
func TestOpenFileAt_ReadsTheBlob(t *testing.T) {
	uc, q, st := uploadUC(t)
	id := uuid.Must(uuid.NewV7())
	st.objects[mediaModel.BlobKey("abc")] = []byte("zip bytes here")
	q.EXPECT().GetFileByID(gomock.Any(), id).Return(postgres.File{ID: id, Name: "a.zip", SizeBytes: 14, ContentHash: "abc"}, nil)
	r, f, err := uc.OpenFileAt(context.Background(), id)
	if err != nil || f.SizeBytes != 14 {
		t.Fatalf("file %+v err %v", f, err)
	}
	if got, err := readAt(t, r, 4, 5); got != "bytes" || err != nil {
		t.Fatalf("%q %v", got, err)
	}
}

// A chunk object that cannot be read is a platform failure (typed, internal): its key never reaches a user.
func TestOpenUploadAt_StorageFailureIsAPlatformError(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	stored(st, id, []byte("0123456789"))
	delete(st.objects, mediaModel.ChunkKey(id, 1))
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(archiveRow(id, owner, 10, 3), nil)
	r, _, err := uc.OpenUploadAt(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readAt(t, r, 4, 2)
	var typed appErr.Error
	if !errors.As(err, &typed) || !typed.StatusCode().IsInternal() {
		t.Fatalf("err %v, want a typed internal error", err)
	}
	if strings.Contains(typed.UnwrapNotInternalError().StatusCode().Message(), "media/uploads") {
		t.Fatalf("message names the key: %s", typed.StatusCode().Message())
	}
}
