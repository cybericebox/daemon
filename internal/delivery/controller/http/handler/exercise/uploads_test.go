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
	aborted  bool
	complete mediaModel.File
}

func (c *chunkedUC) UploadChunkBytes() int64 { return 4 }
func (c *chunkedUC) StartUpload(_ context.Context, name, contentType string, size int64, by uuid.UUID) (mediaModel.Upload, error) {
	c.started, c.owner = [3]any{name, contentType, size}, by
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
	return mediaModel.NewUpload(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "big.bin", "application/octet-stream", 10, 4, time.Hour, time.Now())
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
