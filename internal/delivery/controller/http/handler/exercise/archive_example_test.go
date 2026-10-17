package exercise_test

import (
	"bytes"
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

func TestImportExample_ServesZip(t *testing.T) {
	want, err := exerciseUseCase.ExampleArchive()
	if err != nil {
		t.Fatal(err)
	}
	r := newEngine(&fakeUC{}, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/import/example", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("content type %q", got)
	}
	wantDisposition := mime.FormatMediaType("attachment", map[string]string{"filename": exerciseUseCase.ExampleArchiveFilename})
	if got := w.Header().Get("Content-Disposition"); got != wantDisposition {
		t.Fatalf("disposition %q, want %q", got, wantDisposition)
	}
	if !bytes.Equal(w.Body.Bytes(), want) {
		t.Fatalf("body differs from ExampleArchive (%d vs %d bytes)", w.Body.Len(), len(want))
	}
}

func TestImportExample_RequiresWritePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	exerciseHandler.NewExerciseAPIHandler(&fakeUC{}, roleProt{role: rbac.RoleUser}).Init(r.Group("api"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/import/example", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", w.Code)
	}
}
