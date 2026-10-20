package exercise_test

import (
	"encoding/json"
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

func TestImportExampleDocument_ServesBothForms(t *testing.T) {
	wantYAML, wantJSON, err := exerciseUseCase.ExampleDocuments()
	if err != nil {
		t.Fatal(err)
	}
	r := newEngine(&fakeUC{}, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/import/example/document", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct{ Yaml, Json string }
	}
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %s: %v", w.Body.String(), err)
	}
	if body.Data.Yaml != wantYAML || body.Data.Json != wantJSON {
		t.Fatalf("the answer differs from ExampleDocuments: %s", w.Body.String()[:200])
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control %q, want private, no-store", got)
	}
}

func TestImportExampleDocument_RequiresWritePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	exerciseHandler.NewExerciseAPIHandler(&fakeUC{}, roleProt{role: rbac.RoleUser}).Init(r.Group("api"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/import/example/document", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", w.Code)
	}
}
