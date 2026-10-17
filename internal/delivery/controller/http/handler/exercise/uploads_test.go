package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/gin-gonic/gin"

	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// chunkedUC is a fakeUC with the resumable upload recorded.
type chunkedUC struct {
	fakeUC
	upload   mediaModel.Upload
	err      error
	chunk    []byte
	index    int
	owner    uuid.UUID
	sha      string
	started  [3]any
	purpose  mediaModel.UploadPurpose
	aborted  bool
	complete mediaModel.File
	// noRegistry is what RequireImageUploads answers (nil: the platform registry is configured).
	noRegistry error
	required   int
}

func (c *chunkedUC) RequireImageUploads(context.Context) error {
	c.required++
	return c.noRegistry
}

func (c *chunkedUC) UploadChunkBytes() int64 { return 4 }
func (c *chunkedUC) StartUpload(_ context.Context, name, contentType string, size int64, by uuid.UUID, purpose mediaModel.UploadPurpose) (mediaModel.Upload, error) {
	c.started, c.owner, c.purpose = [3]any{name, contentType, size}, by, purpose
	return c.upload, c.err
}
func (c *chunkedUC) UploadStatus(_ context.Context, _, owner uuid.UUID) (mediaModel.Upload, error) {
	c.owner = owner
	return c.upload, c.err
}
func (c *chunkedUC) PutChunk(_ context.Context, _, owner uuid.UUID, index int, r io.Reader) (mediaModel.Upload, error) {
	c.owner, c.index = owner, index
	var err error
	if c.chunk, err = io.ReadAll(r); err != nil {
		return mediaModel.Upload{}, err
	}
	return c.upload, c.err
}
func (c *chunkedUC) CompleteUpload(_ context.Context, _, owner uuid.UUID, sha string) (mediaModel.File, error) {
	c.owner, c.sha = owner, sha
	return c.complete, c.err
}
func (c *chunkedUC) AbortUpload(_ context.Context, _, owner uuid.UUID) error {
	c.owner, c.aborted = owner, true
	return c.err
}

func sampleUpload() mediaModel.Upload {
	return mediaModel.NewUpload(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "big.bin", "application/octet-stream", 10, 4, time.Hour, time.Now(), mediaModel.UploadPurposeFile)
}

func newChunkedEngine(uc *chunkedUC, uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler, injectIdentity(uid))
	exerciseHandler.NewExerciseAPIHandler(uc, fakeProt{}).Init(r.Group("api"))
	return r
}

func TestChunkedUpload_StartStatusChunkComplete(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	up := sampleUpload()
	up.ChunksReceived = 1
	file := mediaModel.File{ID: uuid.Must(uuid.NewV7()), Name: "big.bin", SizeBytes: 10}
	uc := &chunkedUC{upload: up, complete: file}
	r := newChunkedEngine(uc, uid)

	send := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}

	w := send(http.MethodPost, "/api/exercises/uploads", `{"Name":"big.bin","ContentType":"application/octet-stream","Size":10}`)
	if w.Code != http.StatusOK || uc.started != [3]any{"big.bin", "application/octet-stream", int64(10)} || uc.owner != uid {
		t.Fatalf("start: %d %s started=%v", w.Code, w.Body.String(), uc.started)
	}
	var env struct {
		Data struct {
			UploadID                       uuid.UUID
			Size, ChunkSize, ReceivedBytes int64
			Chunks, ChunksReceived         int
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.UploadID != up.ID || env.Data.Chunks != 3 || env.Data.ChunkSize != 4 || env.Data.ChunksReceived != 1 || env.Data.ReceivedBytes != 4 {
		t.Fatalf("response %+v", env.Data)
	}

	if w = send(http.MethodGet, "/api/exercises/uploads/"+up.ID.String(), ""); w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}

	w = send(http.MethodPut, "/api/exercises/uploads/"+up.ID.String()+"/chunks/1", "abcd")
	if w.Code != http.StatusOK || uc.index != 1 || !bytes.Equal(uc.chunk, []byte("abcd")) || uc.owner != uid {
		t.Fatalf("chunk: %d %s index=%d body=%q", w.Code, w.Body.String(), uc.index, uc.chunk)
	}

	if w = send(http.MethodPost, "/api/exercises/uploads/"+up.ID.String()+"/complete", `{"SHA256":"zz"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a malformed hash must be refused: %d", w.Code)
	}
	hash := strings.Repeat("ab", 32)
	w = send(http.MethodPost, "/api/exercises/uploads/"+up.ID.String()+"/complete", `{"SHA256":"`+hash+`"}`)
	if w.Code != http.StatusOK || uc.sha != hash || !strings.Contains(w.Body.String(), file.ID.String()) {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}

	if w = send(http.MethodDelete, "/api/exercises/uploads/"+up.ID.String(), ""); w.Code != http.StatusOK || !uc.aborted {
		t.Fatalf("abort: %d", w.Code)
	}
}

func TestChunkedUpload_ABodyOverTheChunkLimitIsRefusedAtTheBoundary(t *testing.T) {
	uc := &chunkedUC{upload: sampleUpload()}
	r := newChunkedEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/exercises/uploads/"+uc.upload.ID.String()+"/chunks/0", strings.NewReader("abcdefgh")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("an oversized chunk must be a 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestChunkedUpload_AConflictAnswersWithTheStatusCode(t *testing.T) {
	uc := &chunkedUC{upload: sampleUpload(), err: mediaModel.ErrUploadChunkOutOfOrder.Err()}
	r := newChunkedEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/exercises/uploads/"+uc.upload.ID.String()+"/chunks/2", strings.NewReader("abcd")))
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", w.Code)
	}
}

// Purpose "image-blob" reaches the use case (it raises the file limit to an image archive's); another value is refused.
func TestChunkedUpload_StartPassesThePurpose(t *testing.T) {
	uc := &chunkedUC{upload: sampleUpload()}
	r := newChunkedEngine(uc, uuid.Must(uuid.NewV7()))
	start := func(body string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/exercises/uploads", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := start(`{"Name":"sha256-x","Size":10,"Purpose":"image-blob"}`); code != http.StatusOK || uc.purpose != mediaModel.UploadPurposeImageBlob {
		t.Fatalf("image blob: %d %q", code, uc.purpose)
	}
	if code := start(`{"Name":"a.bin","Size":10}`); code != http.StatusOK || uc.purpose != mediaModel.UploadPurposeFile {
		t.Fatalf("file: %d %q", code, uc.purpose)
	}
	uc.purpose = "unchanged"
	if code := start(`{"Name":"a.bin","Size":10,"Purpose":"avatar"}`); code != http.StatusBadRequest || uc.purpose != "unchanged" {
		t.Fatalf("unknown purpose: %d", code)
	}
}

// An image-blob upload is refused before it starts while the platform registry is not configured: the
// archive would be uploaded for nothing. Ordinary files do not ask.
func TestChunkedUpload_ImageBlobNeedsTheRegistry(t *testing.T) {
	uc := &chunkedUC{upload: sampleUpload(), noRegistry: imageRegistryModel.ErrRegistryNotConfigured.Err()}
	r := newChunkedEngine(uc, uuid.Must(uuid.NewV7()))
	start := func(body string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/exercises/uploads", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := start(`{"Name":"sha256-x","Size":10,"Purpose":"image-blob"}`); code == http.StatusOK || uc.started[0] != nil {
		t.Fatalf("image blob without a registry: %d started=%v", code, uc.started)
	}
	if code := start(`{"Name":"a.bin","Size":10}`); code != http.StatusOK || uc.required != 1 {
		t.Fatalf("file: %d, registry asked %d times", code, uc.required)
	}
}
