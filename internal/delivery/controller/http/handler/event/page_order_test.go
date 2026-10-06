package event_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type pageDraftContractUC struct {
	eventHandler.IUseCase
	saved          *eventUseCase.EventPageInput
	published      uuid.UUID
	discarded      uuid.UUID
	landingActions []string
}

func (u *pageDraftContractUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *pageDraftContractUC) SaveEventPageDraft(_ context.Context, _, pageID uuid.UUID, in eventUseCase.EventPageInput) (eventUseCase.EventPageView, error) {
	u.saved = &in
	return eventUseCase.EventPageView{ID: pageID}, nil
}

func (u *pageDraftContractUC) PublishEventPage(_ context.Context, _, pageID uuid.UUID) (eventUseCase.EventPageView, error) {
	u.published = pageID
	return eventUseCase.EventPageView{ID: pageID}, nil
}

func (u *pageDraftContractUC) DiscardEventPageDraft(_ context.Context, _, pageID uuid.UUID) error {
	u.discarded = pageID
	return nil
}

func (u *pageDraftContractUC) SaveLandingDraft(context.Context, uuid.UUID, eventContentModel.Document) error {
	u.landingActions = append(u.landingActions, "save")
	return nil
}

func (u *pageDraftContractUC) PublishLanding(context.Context, uuid.UUID) error {
	u.landingActions = append(u.landingActions, "publish")
	return nil
}

func (u *pageDraftContractUC) DiscardLandingDraft(context.Context, uuid.UUID) error {
	u.landingActions = append(u.landingActions, "discard")
	return nil
}

func serve(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestPageDraftRoutesSavePublishAndDiscard(t *testing.T) {
	actor, eventID, pageID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &pageDraftContractUC{}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/pages/" + pageID.String()

	if response := serve(t, router, http.MethodPut, base, `{"Slug":"rules","Title":"Rules","Document":{"blocks":[]},"Visibility":0,"Navigation":1,"NavigationAfter":"first"}`); response.Code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", response.Code, response.Body.String())
	}
	if u.saved == nil || u.saved.NavigationAfter != "first" || u.saved.Slug != "rules" {
		t.Fatalf("draft input lost: %+v", u.saved)
	}
	if response := serve(t, router, http.MethodPost, base+"/publish", ""); response.Code != http.StatusOK || u.published != pageID {
		t.Fatalf("publish status=%d body=%s", response.Code, response.Body.String())
	}
	if response := serve(t, router, http.MethodDelete, base+"/draft", ""); response.Code != http.StatusNoContent || u.discarded != pageID {
		t.Fatalf("discard status=%d body=%s", response.Code, response.Body.String())
	}
	if response := serve(t, router, http.MethodPut, "/api/events/"+eventID.String()+"/manage/pages/order", `{"PageIDs":[]}`); response.Code != http.StatusBadRequest {
		t.Fatalf("the separate order route is gone, status=%d", response.Code)
	}
}

func TestLandingDraftRoutesSavePublishAndDiscard(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &pageDraftContractUC{}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/content/landing"
	for _, step := range []struct{ method, path, body string }{
		{http.MethodPut, base, `{"Document":{"blocks":[]}}`},
		{http.MethodPost, base + "/publish", ""},
		{http.MethodDelete, base + "/draft", ""},
	} {
		if response := serve(t, router, step.method, step.path, step.body); response.Code != http.StatusNoContent {
			t.Fatalf("%s %s status=%d body=%s", step.method, step.path, response.Code, response.Body.String())
		}
	}
	if strings.Join(u.landingActions, ",") != "save,publish,discard" {
		t.Fatalf("landing actions = %v", u.landingActions)
	}
}
