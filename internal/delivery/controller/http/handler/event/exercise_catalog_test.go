package event_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type catalogContractUC struct {
	eventHandler.IUseCase
	readErr error
	search  string
	items   []eventUseCase.PublishedExerciseChoice
	// W4 filters
	infrastructure string
	tags           []string
	tagItems       []eventUseCase.EventCatalogTag
	tagPrefix      string
	tagLimit       int
	variant        int
	preview        eventUseCase.PublishedExercisePreview
}

func (u *catalogContractUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return u.readErr
}

func (u *catalogContractUC) ListPublishedExercisesForEvent(_ context.Context, _ uuid.UUID, search, infrastructure string, tags []string) ([]eventUseCase.PublishedExerciseChoice, error) {
	u.search, u.infrastructure, u.tags = search, infrastructure, tags
	return u.items, nil
}

func (u *catalogContractUC) ListEventCatalogTags(_ context.Context, _ uuid.UUID, prefix string, limit int) ([]eventUseCase.EventCatalogTag, error) {
	u.tagPrefix, u.tagLimit = prefix, limit
	return u.tagItems, nil
}

func (u *catalogContractUC) GetPublishedExercisePreviewForEvent(_ context.Context, _, _ uuid.UUID, variant int) (eventUseCase.PublishedExercisePreview, error) {
	u.variant = variant
	return u.preview, nil
}

func TestEventCatalogIsScopedToEventReadAccess(t *testing.T) {
	actor, eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	url := "/api/events/" + eventID.String() + "/manage/exercise-catalog?search=web"
	u := &catalogContractUC{items: []eventUseCase.PublishedExerciseChoice{{ID: exerciseID, Name: "Web", PublishedVersionID: versionID}}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK || u.search != "web" {
		t.Fatalf("status=%d search=%q body=%s", w.Code, u.search, w.Body.String())
	}
	var body struct {
		Data []struct{ ID, Name, PublishedVersionID string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].ID != exerciseID.String() || body.Data[0].PublishedVersionID != versionID.String() {
		t.Fatalf("catalog response=%s error=%v", w.Body.String(), err)
	}
	u.readErr = eventManagerModel.ErrEventManagementForbidden.Err()
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEventCatalogPreviewIsScopedAndOmitsSecrets(t *testing.T) {
	actor, eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	url := "/api/events/" + eventID.String() + "/manage/exercise-catalog/" + versionID.String()
	u := &catalogContractUC{preview: eventUseCase.PublishedExercisePreview{ID: exerciseID, Name: "Web", VersionID: versionID, VariantCount: 1, Tasks: []eventUseCase.PublishedExerciseTaskPreview{{Name: "Find it", Difficulty: "easy"}}}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !json.Valid(w.Body.Bytes()) || !bytes.Contains(w.Body.Bytes(), []byte(`"VariantCount":1`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"Name":"Find it"`)) || bytes.Contains(w.Body.Bytes(), []byte(`"Flag"`)) {
		t.Fatalf("unexpected preview body=%s", w.Body.String())
	}
	u.readErr = eventManagerModel.ErrEventManagementForbidden.Err()
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEventCatalogPassesRepeatedTagsAndInfrastructure(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	url := "/api/events/" + eventID.String() + "/manage/exercise-catalog?search=web&infrastructure=yes&tags=web&tags=crypto"
	u := &catalogContractUC{}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK || u.search != "web" || u.infrastructure != "yes" || len(u.tags) != 2 || u.tags[0] != "web" || u.tags[1] != "crypto" {
		t.Fatalf("status=%d search=%q infra=%q tags=%v body=%s", w.Code, u.search, u.infrastructure, u.tags, w.Body.String())
	}
}

func TestEventCatalogTagsAreScopedAndShaped(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	base := "/api/events/" + eventID.String() + "/manage/exercise-catalog/tags"
	u := &catalogContractUC{tagItems: []eventUseCase.EventCatalogTag{{Tag: "web", ExerciseCount: 3}}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"?prefix=we&limit=20", nil))
	var body struct {
		Data []struct {
			Tag           string
			ExerciseCount int64
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); w.Code != http.StatusOK || err != nil || len(body.Data) != 1 || body.Data[0].Tag != "web" || body.Data[0].ExerciseCount != 3 || u.tagPrefix != "we" || u.tagLimit != 20 {
		t.Fatalf("status=%d prefix=%q limit=%d body=%s err=%v", w.Code, u.tagPrefix, u.tagLimit, w.Body.String(), err)
	}

	for _, bad := range []string{"?limit=abc", "?limit=0", "?limit=-1"} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+bad, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", bad, w.Code)
		}
	}

	u.readErr = eventManagerModel.ErrEventManagementForbidden.Err()
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base, nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d body=%s", w.Code, w.Body.String())
	}
}
