package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type liveContractUC struct {
	eventHandler.IUseCase
	published eventContentModel.LiveLayout
	draft     *eventContentModel.LiveLayout
}

func (*liveContractUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error   { return nil }
func (*liveContractUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (u *liveContractUC) GetLiveLayoutEditor(context.Context, uuid.UUID) (eventUseCase.LiveLayoutEditorView, error) {
	return eventUseCase.LiveLayoutEditorView{Published: u.published, Draft: u.draft}, nil
}

func (u *liveContractUC) SaveLiveLayoutDraft(_ context.Context, _ uuid.UUID, layout eventContentModel.LiveLayout) error {
	u.draft = &layout
	return nil
}

func (u *liveContractUC) PublishLiveLayout(context.Context, uuid.UUID) (eventContentModel.LiveLayout, error) {
	u.published = *u.draft
	u.published.Version++
	u.draft = nil
	return u.published, nil
}

func TestLiveLayoutHTTPContractForEditor(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &liveContractUC{published: eventContentModel.DefaultLiveLayout()}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/content/live"

	read := eventRequest(t, router, http.MethodGet, base, "")
	var initial eventUseCase.LiveLayoutEditorView
	if err := json.Unmarshal(read["Data"], &initial); err != nil || initial.Published.Version != 1 || initial.Draft != nil {
		t.Fatalf("initial editor payload: %s error=%v", read["Data"], err)
	}

	draft := eventContentModel.DefaultLiveLayout()
	draft.Theme = "light"
	body, err := json.Marshal(struct {
		Layout eventContentModel.LiveLayout `json:"Layout"`
	}{Layout: draft})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, base, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	write := httptest.NewRecorder()
	router.ServeHTTP(write, request)
	if write.Code != http.StatusNoContent || u.draft == nil || u.draft.Theme != "light" {
		t.Fatalf("save draft: status=%d body=%s draft=%+v", write.Code, write.Body.String(), u.draft)
	}

	read = eventRequest(t, router, http.MethodGet, base, "")
	var pending eventUseCase.LiveLayoutEditorView
	if err := json.Unmarshal(read["Data"], &pending); err != nil || pending.Draft == nil || pending.Published.Theme != "dark" {
		t.Fatalf("pending editor payload: %s error=%v", read["Data"], err)
	}

	result := eventRequest(t, router, http.MethodPost, base+"/publish", "")
	var published eventContentModel.LiveLayout
	if err := json.Unmarshal(result["Data"], &published); err != nil || published.Version != 2 || published.Theme != "light" {
		t.Fatalf("publish payload: %s error=%v", result["Data"], err)
	}

	// Open live screens poll the version and reload the layout on a change.
	version := eventRequest(t, router, http.MethodGet, base+"/version", "")
	if string(version["Data"]) != `{"Version":2}` {
		t.Fatalf("version payload: %s", version["Data"])
	}
}
