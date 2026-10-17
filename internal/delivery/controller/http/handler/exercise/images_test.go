package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

func (f *fakeUC) CheckImageAvailability(_ context.Context, scope imageRegistryUseCase.Scope, images []string) ([]imageRegistryModel.Availability, error) {
	f.imageScope, f.checkedImages = scope, images
	return f.availability, f.err
}

func postImages(r *gin.Engine, exID uuid.UUID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/images/check", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCheckImagesUsesTheOwnerEventAndReturnsTheResults(t *testing.T) {
	exID, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		exerciseView: exerciseUseCase.ExerciseView{ID: exID, OwnerEventID: &owner},
		availability: []imageRegistryModel.Availability{
			{Image: "nginx:1", Status: imageRegistryModel.StatusAvailable},
			{Image: "ghcr.io/acme/x:1", Status: imageRegistryModel.StatusUnavailable, Reason: imageRegistryModel.ReasonUnauthorized},
		},
	}
	w := postImages(newEngine(uc, uuid.Must(uuid.NewV7())), exID, `{"Images":["nginx:1","ghcr.io/acme/x:1"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if uc.imageScope.EventID == nil || *uc.imageScope.EventID != owner || len(uc.checkedImages) != 2 {
		t.Fatalf("scope %+v images %v", uc.imageScope, uc.checkedImages)
	}
	var body struct{ Data []map[string]string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[1]["Image"] != "ghcr.io/acme/x:1" || body.Data[1]["Status"] != "unavailable" || body.Data[1]["Reason"] != "unauthorized" {
		t.Fatalf("body %s", w.Body.String())
	}
}

func TestCheckImagesCatalogExerciseHasNoEvent(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{ID: exID}}
	if w := postImages(newEngine(uc, uuid.Must(uuid.NewV7())), exID, `{"Images":["nginx:1"]}`); w.Code != http.StatusOK || uc.imageScope.EventID != nil {
		t.Fatalf("status %d scope %+v", w.Code, uc.imageScope)
	}
}

func TestCheckImagesNeedsEditAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	uc := &fakeUC{}
	exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: rbac.RoleUser}).Init(r.Group("api"))
	if w := postImages(r, uuid.Must(uuid.NewV7()), `{"Images":["nginx:1"]}`); w.Code != http.StatusForbidden || uc.checkedImages != nil {
		t.Fatalf("status %d checked %v", w.Code, uc.checkedImages)
	}
}

func TestCheckImagesRefusesABadBody(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	many := `{"Images":["` + strings.Repeat(`a","`, 200) + `a"]}`
	for _, body := range []string{`{`, many, `{"Images":["` + strings.Repeat("a", 600) + `"]}`} {
		if w := postImages(r, exID, body); w.Code != http.StatusBadRequest {
			t.Fatalf("status %d for %.40s", w.Code, body)
		}
	}
	if uc.checkedImages != nil {
		t.Fatal("a bad body reaches no check")
	}
}
