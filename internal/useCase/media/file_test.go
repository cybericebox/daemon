package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	media "github.com/cybericebox/daemon/internal/useCase/media"
)

// fakeStorage records puts/copies in-memory.
type fakeStorage struct {
	objects map[string][]byte
	copies  [][2]string
}

func newFakeStorage() *fakeStorage { return &fakeStorage{objects: map[string][]byte{}} }

func (f *fakeStorage) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objects[key] = b
	return nil
}
func (f *fakeStorage) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	b, ok := f.objects[key]
	if !ok {
		return nil, "", errors.New("missing")
	}
	return io.NopCloser(bytes.NewReader(b)), "", nil
}
func (f *fakeStorage) Remove(_ context.Context, key string) error { delete(f.objects, key); return nil }
func (f *fakeStorage) Stat(_ context.Context, key string) (bool, error) {
	_, ok := f.objects[key]
	return ok, nil
}
func (f *fakeStorage) Copy(_ context.Context, src, dst string) error {
	f.objects[dst] = f.objects[src]
	f.copies = append(f.copies, [2]string{src, dst})
	return nil
}

func newUC(t *testing.T, q media.IRepository, maxBytes int64) *media.MediaUseCase {
	t.Helper()
	return media.NewMediaUseCase(media.Dependencies{
		Repo:    q,
		Storage: newFakeStorage(),
		Config:  config.MediaConfig{MaxUploadBytes: maxBytes},
	})
}

func TestUploadFile_HashesAndRecords(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	st := newFakeStorage()
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: st, Config: config.MediaConfig{MaxUploadBytes: 1024}})

	payload := []byte("attachment-bytes")
	wantHash := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(wantHash[:])

	q.EXPECT().CreateFile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateFileParams) (postgres.File, error) {
			if arg.ContentHash != wantHex {
				t.Fatalf("hash mismatch: %s", arg.ContentHash)
			}
			if arg.SizeBytes != int64(len(payload)) {
				t.Fatalf("size mismatch: %d", arg.SizeBytes)
			}
			return postgres.File{ID: arg.ID, Name: arg.Name, ContentHash: arg.ContentHash, SizeBytes: arg.SizeBytes}, nil
		})

	f, err := uc.UploadFile(context.Background(), "dump.pcap", "application/octet-stream", bytes.NewReader(payload), uuid.Nil)
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if _, ok := st.objects[mediaModel.BlobKey(wantHex)]; !ok {
		t.Fatal("blob object must exist after upload")
	}
	if len(st.objects) != 1 {
		t.Fatalf("tmp object must be removed, got %d objects", len(st.objects))
	}
	if f.ContentHash != wantHex {
		t.Fatalf("returned hash mismatch: %s", f.ContentHash)
	}
}

func TestUploadFile_TooLarge(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl) // CreateFile must NOT be called
	uc := newUC(t, q, 8)

	_, err := uc.UploadFile(context.Background(), "big.bin", "application/octet-stream",
		bytes.NewReader(bytes.Repeat([]byte("x"), 9)), uuid.Nil)
	if !errors.Is(err, mediaModel.ErrFileTooLarge.Err()) {
		t.Fatalf("want ErrFileTooLarge, got %v", err)
	}
}

func TestStreamFile_StorageUnconfigured(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl) // GetFileByID must NOT be called
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: nil, Config: config.MediaConfig{MaxUploadBytes: 8}})

	rc, _, err := uc.StreamFile(context.Background(), uuid.Must(uuid.NewV7()))
	if err == nil {
		t.Fatal("want error for unconfigured storage, got nil")
	}
	if rc != nil {
		t.Fatal("reader must be nil on error")
	}
}

func TestUploadFile_StorageUnconfigured(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: nil, Config: config.MediaConfig{MaxUploadBytes: 8}})
	_, err := uc.UploadFile(context.Background(), "a", "b", bytes.NewReader(nil), uuid.Nil)
	if !errors.Is(err, mediaModel.ErrMediaStorageNotConfigured.Err()) {
		t.Fatalf("want ErrMediaStorageNotConfigured, got %v", err)
	}
}

func TestGetFile_ReturnsMetadataWithoutStorage(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: nil})
	id := uuid.Must(uuid.NewV7())
	q.EXPECT().GetFileByID(gomock.Any(), id).Return(postgres.File{ID: id, ContentType: "image/png", SizeBytes: 42}, nil)

	f, err := uc.GetFile(context.Background(), id)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if f.ID != id || f.SizeBytes != 42 || f.ContentType != "image/png" {
		t.Fatalf("unexpected file %+v", f)
	}
}

func TestGetFile_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: nil})
	q.EXPECT().GetFileByID(gomock.Any(), gomock.Any()).Return(postgres.File{}, pgx.ErrNoRows)

	_, err := uc.GetFile(context.Background(), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, mediaModel.ErrFileNotFound.Err()) {
		t.Fatalf("want ErrFileNotFound, got %v", err)
	}
}
