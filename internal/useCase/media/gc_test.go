package media_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	media "github.com/cybericebox/daemon/internal/useCase/media"
)

func TestCleanupOrphanFiles_RemovesBlobsFromStorage(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	st := newFakeStorage()
	st.objects[mediaModel.BlobKey("dead-hash")] = []byte("x")

	uc := media.NewMediaUseCase(media.Dependencies{
		Repo: q, Storage: st,
		Config: config.MediaConfig{MaxUploadBytes: 1, GCGrace: 24 * time.Hour},
	})

	q.EXPECT().ListExpiredMediaUploads(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().DeleteUnreferencedFiles(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListOrphanBlobs(gomock.Any(), gomock.Any()).Return([]string{"dead-hash"}, nil)
	q.EXPECT().DeleteBlob(gomock.Any(), "dead-hash").Return(int64(1), nil)

	if err := uc.CleanupOrphanFiles(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanFiles: %v", err)
	}
	if _, ok := st.objects[mediaModel.BlobKey("dead-hash")]; ok {
		t.Fatal("orphan blob object must be removed from storage")
	}
}

// TestCleanupOrphanFiles_SameGraceCutoffForFilesAndBlobs locks in that the
// blob-level touch grace (ListOrphanBlobs) uses the SAME cutoff as the
// files-level grace (DeleteUnreferencedFiles) — both derived from one
// time.Now().Add(-GCGrace) computed once per pass, not two independent
// "now" calls that could drift apart.
func TestCleanupOrphanFiles_SameGraceCutoffForFilesAndBlobs(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	st := newFakeStorage()

	uc := media.NewMediaUseCase(media.Dependencies{
		Repo: q, Storage: st,
		Config: config.MediaConfig{MaxUploadBytes: 1, GCGrace: 24 * time.Hour},
	})

	var filesCutoff, blobsCutoff time.Time
	q.EXPECT().ListExpiredMediaUploads(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().DeleteUnreferencedFiles(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, createdBefore time.Time) ([]postgres.DeleteUnreferencedFilesRow, error) {
			filesCutoff = createdBefore
			return nil, nil
		},
	)
	q.EXPECT().ListOrphanBlobs(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, touchedBefore time.Time) ([]string, error) {
			blobsCutoff = touchedBefore
			return nil, nil
		},
	)

	if err := uc.CleanupOrphanFiles(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanFiles: %v", err)
	}
	if filesCutoff.IsZero() || blobsCutoff.IsZero() {
		t.Fatalf("expected both cutoffs to be captured: files=%v blobs=%v", filesCutoff, blobsCutoff)
	}
	if !filesCutoff.Equal(blobsCutoff) {
		t.Fatalf("files and blobs GC cutoffs must be the same instant: files=%v blobs=%v", filesCutoff, blobsCutoff)
	}
}
