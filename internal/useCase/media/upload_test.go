package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	media "github.com/cybericebox/daemon/internal/useCase/media"
)

var uploadCfg = config.MediaConfig{MaxUploadBytes: 100, UploadChunkBytes: 4, UploadTTL: time.Hour}

func uploadUC(t *testing.T) (*media.MediaUseCase, *postgresMocks.MockQuerier, *fakeStorage) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	st := newFakeStorage()
	return media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: st, Config: uploadCfg}), q, st
}

func row(id, owner uuid.UUID, size int64, received int32) postgres.MediaUpload {
	return postgres.MediaUpload{
		ID: id, CreatedBy: owner, Name: "dump.bin", ContentType: "application/octet-stream", SizeBytes: size, ChunkBytes: 4,
		ChunksReceived: received, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
}

func TestStartUpload_RefusesOverTheFileLimitAndBadInput(t *testing.T) {
	uc, _, _ := uploadUC(t)
	owner := uuid.Must(uuid.NewV7())
	if _, err := uc.StartUpload(context.Background(), "a", "", 101, owner); !errors.Is(err, mediaModel.ErrFileTooLarge.Err()) {
		t.Fatalf("over the limit: %v", err)
	}
	for _, c := range []struct {
		name string
		size int64
	}{{"", 10}, {"  ", 10}, {"a", 0}} {
		if _, err := uc.StartUpload(context.Background(), c.name, "", c.size, owner); !errors.Is(err, mediaModel.ErrUploadInvalid.Err()) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}

func TestStartUpload_FileLargerThanOneRequestIsFine(t *testing.T) {
	uc, q, _ := uploadUC(t)
	owner := uuid.Must(uuid.NewV7())
	q.EXPECT().CreateMediaUpload(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateMediaUploadParams) (postgres.MediaUpload, error) {
		if p.SizeBytes != 90 || p.ChunkBytes != 4 || p.CreatedBy != owner || !p.ExpiresAt.After(p.CreatedAt) {
			t.Fatalf("params %+v", p)
		}
		return postgres.MediaUpload{ID: p.ID, CreatedBy: owner, SizeBytes: p.SizeBytes, ChunkBytes: p.ChunkBytes, ExpiresAt: p.ExpiresAt}, nil
	})
	up, err := uc.StartUpload(context.Background(), "big.bin", "application/octet-stream", 90, owner)
	if err != nil || up.ChunkCount() != 23 {
		t.Fatalf("upload %+v err %v", up, err)
	}
}

func TestUploadStatus_ForeignAndExpiredAndAbsentAreTheSame(t *testing.T) {
	uc, q, _ := uploadUC(t)
	id, owner, stranger := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expired := row(id, owner, 10, 0)
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 0), nil)
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(expired, nil)
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(postgres.MediaUpload{}, pgx.ErrNoRows)
	for i, who := range []uuid.UUID{stranger, owner, owner} {
		if _, err := uc.UploadStatus(context.Background(), id, who); !errors.Is(err, mediaModel.ErrUploadNotFound.Err()) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestPutChunk_StoresInOrderAndAdvances(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 1), nil)
	q.EXPECT().AdvanceMediaUpload(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.AdvanceMediaUploadParams) (int64, error) {
		if p.ExpectedChunks != 1 || p.CreatedBy != owner || p.ID != id {
			t.Fatalf("params %+v", p)
		}
		return 1, nil
	})
	up, err := uc.PutChunk(context.Background(), id, owner, 1, bytes.NewReader([]byte("abcd")))
	if err != nil || up.ChunksReceived != 2 {
		t.Fatalf("upload %+v err %v", up, err)
	}
	if string(st.objects[mediaModel.ChunkKey(id, 1)]) != "abcd" {
		t.Fatal("chunk not stored")
	}
}

func TestPutChunk_RepeatedChunkIsAcceptedWithoutStoring(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 2), nil)
	up, err := uc.PutChunk(context.Background(), id, owner, 0, bytes.NewReader([]byte("abcd")))
	if err != nil || up.ChunksReceived != 2 || len(st.objects) != 0 {
		t.Fatalf("upload %+v err %v objects %d", up, err, len(st.objects))
	}
}

func TestPutChunk_OutOfOrderAndWrongSize(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 1), nil).Times(4)
	if _, err := uc.PutChunk(context.Background(), id, owner, 2, bytes.NewReader([]byte("abcd"))); !errors.Is(err, mediaModel.ErrUploadChunkOutOfOrder.Err()) {
		t.Fatalf("gap: %v", err)
	}
	if _, err := uc.PutChunk(context.Background(), id, owner, 3, bytes.NewReader([]byte("abcd"))); !errors.Is(err, mediaModel.ErrUploadChunkOutOfOrder.Err()) {
		t.Fatalf("past the end: %v", err)
	}
	if _, err := uc.PutChunk(context.Background(), id, owner, 1, bytes.NewReader([]byte("abc"))); !errors.Is(err, mediaModel.ErrUploadChunkSize.Err()) {
		t.Fatalf("short: %v", err)
	}
	if _, err := uc.PutChunk(context.Background(), id, owner, 1, bytes.NewReader([]byte("abcde"))); !errors.Is(err, mediaModel.ErrUploadChunkSize.Err()) {
		t.Fatalf("long: %v", err)
	}
	if len(st.objects) != 0 {
		t.Fatalf("a refused chunk must not stay stored: %d objects", len(st.objects))
	}
}

func TestPutChunk_TheLastChunkIsTheRemainder(t *testing.T) {
	uc, q, _ := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 2), nil)
	q.EXPECT().AdvanceMediaUpload(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	up, err := uc.PutChunk(context.Background(), id, owner, 2, bytes.NewReader([]byte("ef")))
	if err != nil || !up.Complete() {
		t.Fatalf("upload %+v err %v", up, err)
	}
}

func TestPutChunk_ALostRaceIsOutOfOrder(t *testing.T) {
	uc, q, _ := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 0), nil)
	q.EXPECT().AdvanceMediaUpload(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	if _, err := uc.PutChunk(context.Background(), id, owner, 0, bytes.NewReader([]byte("abcd"))); !errors.Is(err, mediaModel.ErrUploadChunkOutOfOrder.Err()) {
		t.Fatalf("%v", err)
	}
}

// stored puts the chunks of payload into the fake storage as PutChunk would have.
func stored(st *fakeStorage, id uuid.UUID, payload []byte) {
	for i := 0; i*4 < len(payload); i++ {
		end := min((i+1)*4, len(payload))
		st.objects[mediaModel.ChunkKey(id, i)] = payload[i*4 : end]
	}
}

func TestCompleteUpload_AssemblesChecksAndRecords(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	payload := []byte("0123456789")
	stored(st, id, payload)
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 3), nil)
	q.EXPECT().CreateFile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateFileParams) (postgres.File, error) {
		if p.ContentHash != hexSum || p.SizeBytes != 10 || p.Name != "dump.bin" || p.CreatedBy.UUID != owner {
			t.Fatalf("params %+v", p)
		}
		return postgres.File{ID: p.ID, Name: p.Name, ContentHash: p.ContentHash, SizeBytes: p.SizeBytes}, nil
	})
	q.EXPECT().DeleteMediaUpload(gomock.Any(), id).Return(int64(1), nil)
	file, err := uc.CompleteUpload(context.Background(), id, owner, hexSum)
	if err != nil || file.ContentHash != hexSum {
		t.Fatalf("file %+v err %v", file, err)
	}
	if string(st.objects[mediaModel.BlobKey(hexSum)]) != string(payload) {
		t.Fatal("the assembled blob must be stored under its hash")
	}
	for i := 0; i < 3; i++ {
		if _, ok := st.objects[mediaModel.ChunkKey(id, i)]; ok {
			t.Fatalf("chunk %d must be removed", i)
		}
	}
}

func TestCompleteUpload_HashMismatchDropsTheUpload(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	stored(st, id, []byte("0123456789"))
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 3), nil)
	q.EXPECT().DeleteMediaUpload(gomock.Any(), id).Return(int64(1), nil)
	_, err := uc.CompleteUpload(context.Background(), id, owner, hex.EncodeToString(make([]byte, 32)))
	if !errors.Is(err, mediaModel.ErrUploadHashMismatch.Err()) {
		t.Fatalf("%v", err)
	}
	if len(st.objects) != 0 {
		t.Fatalf("chunks and the tmp object must be gone, %d left", len(st.objects))
	}
}

func TestCompleteUpload_IncompleteIsRefused(t *testing.T) {
	uc, q, _ := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetMediaUpload(gomock.Any(), id).Return(row(id, owner, 10, 2), nil)
	if _, err := uc.CompleteUpload(context.Background(), id, owner, "x"); !errors.Is(err, mediaModel.ErrUploadIncomplete.Err()) {
		t.Fatalf("%v", err)
	}
}

func TestCleanupExpiredUploads_RemovesChunksAndRows(t *testing.T) {
	uc, q, st := uploadUC(t)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	stored(st, id, []byte("01234567"))
	q.EXPECT().ListExpiredMediaUploads(gomock.Any(), gomock.Any()).Return([]postgres.MediaUpload{row(id, owner, 10, 2)}, nil)
	q.EXPECT().DeleteMediaUpload(gomock.Any(), id).Return(int64(1), nil)
	if err := uc.CleanupExpiredUploads(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.objects) != 0 {
		t.Fatalf("%d chunks left", len(st.objects))
	}
}
