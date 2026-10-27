package exercise_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

func TestCatalogColumnsPreference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &fakeUC{}
	r := gin.New()
	r.Use(response.WithErrorHandler)
	exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: rbac.RoleUser}).Init(r.Group("api"))
	serve := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/exercises/preferences/catalog-columns", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}

	// Never arranged: null, so the frontend shows its default columns.
	if w := serve(http.MethodGet, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Data":null`) {
		t.Fatalf("unset: %d %s", w.Code, w.Body.String())
	}
	if w := serve(http.MethodPut, `[{"key":"status","visible":true,"width":180},{"key":"tags","visible":false}]`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	w := serve(http.MethodGet, "")
	var body struct {
		Data []exerciseUseCase.CatalogColumn
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[0] != (exerciseUseCase.CatalogColumn{Key: "status", Visible: true, Width: 180}) || body.Data[1].Visible || body.Data[1].Width != 0 {
		t.Fatalf("read back: %+v", body.Data)
	}
	for _, bad := range []string{`[{"key":"Status"}]`, `[{"key":"a"},{"key":"a"}]`, `{"key":"a"}`, `[{"key":"` + strings.Repeat("a", 33) + `"}]`, `[{"key":"a","width":59}]`, `[{"key":"a","width":1601}]`} {
		if w := serve(http.MethodPut, bad); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d", bad, w.Code)
		}
	}
	if len(uc.catalogColumns) != 2 {
		t.Fatalf("a refused list must not be stored: %+v", uc.catalogColumns)
	}
}

func TestValidateCatalogColumnsBoundsTheList(t *testing.T) {
	columns := make([]exerciseUseCase.CatalogColumn, exerciseUseCase.MaxCatalogColumns+1)
	for i := range columns {
		columns[i].Key = "c" + strings.Repeat("x", i%20) + string(rune('a'+i%26))
	}
	if exerciseUseCase.ValidateCatalogColumns(columns) == nil {
		t.Fatal("33 columns must be refused")
	}
	if exerciseUseCase.ValidateCatalogColumns(nil) != nil {
		t.Fatal("an empty list is the default")
	}
}
