package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type liveContractScreen struct {
	name      string
	published eventContentModel.LiveLayout
	draft     *eventContentModel.LiveLayout
	link      *eventUseCase.LiveScreenLinkView
}

// liveContractUC keeps the screens in memory; uuid.Nil is the first screen,
// as in the use case.
type liveContractUC struct {
	eventHandler.IUseCase
	order    []uuid.UUID
	screens  map[uuid.UUID]*liveContractScreen
	created  eventUseCase.CreateLiveScreenInput
	withLink bool
}

func newLiveContractUC() *liveContractUC {
	first := uuid.Must(uuid.NewV7())
	return &liveContractUC{order: []uuid.UUID{first}, screens: map[uuid.UUID]*liveContractScreen{first: {name: "Екран 1", published: eventContentModel.DefaultLiveLayout()}}}
}

func (*liveContractUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error   { return nil }
func (*liveContractUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (u *liveContractUC) screen(id uuid.UUID) (uuid.UUID, *liveContractScreen, error) {
	if id == uuid.Nil {
		id = u.order[0]
	}
	screen, ok := u.screens[id]
	if !ok {
		return id, nil, eventContentModel.ErrLiveScreenNotFound.Err()
	}
	return id, screen, nil
}

func (u *liveContractUC) summary(id uuid.UUID) eventUseCase.LiveScreenSummaryView {
	screen := u.screens[id]
	position := 0
	for index, other := range u.order {
		if other == id {
			position = index
		}
	}
	return eventUseCase.LiveScreenSummaryView{ID: id, Name: screen.name, Position: int32(position), Version: screen.published.Version, HasDraft: screen.draft != nil, Link: screen.link}
}

func (u *liveContractUC) ListLiveScreens(_ context.Context, _ uuid.UUID, withLinks bool) ([]eventUseCase.LiveScreenSummaryView, error) {
	u.withLink = withLinks
	out := []eventUseCase.LiveScreenSummaryView{}
	for _, id := range u.order {
		out = append(out, u.summary(id))
	}
	return out, nil
}

func (u *liveContractUC) GetLiveScreenEditor(_ context.Context, _, screenID uuid.UUID) (eventUseCase.LiveScreenEditorView, error) {
	id, screen, err := u.screen(screenID)
	if err != nil {
		return eventUseCase.LiveScreenEditorView{}, err
	}
	return eventUseCase.LiveScreenEditorView{LiveScreenSummaryView: u.summary(id), Published: screen.published, Draft: screen.draft}, nil
}

func (u *liveContractUC) CreateLiveScreen(_ context.Context, _ uuid.UUID, in eventUseCase.CreateLiveScreenInput) (eventUseCase.LiveScreenSummaryView, error) {
	u.created = in
	id := uuid.Must(uuid.NewV7())
	name := in.Name
	if name == "" {
		name = "Екран 2"
	}
	u.screens[id] = &liveContractScreen{name: name, published: eventContentModel.DefaultLiveLayout()}
	u.order = append(u.order, id)
	return u.summary(id), nil
}

func (u *liveContractUC) DuplicateLiveScreen(ctx context.Context, eventID, screenID uuid.UUID, name string) (eventUseCase.LiveScreenSummaryView, error) {
	if _, _, err := u.screen(screenID); err != nil {
		return eventUseCase.LiveScreenSummaryView{}, err
	}
	return u.CreateLiveScreen(ctx, eventID, eventUseCase.CreateLiveScreenInput{Name: name, CopyFrom: &screenID})
}

func (u *liveContractUC) RenameLiveScreen(_ context.Context, _, screenID uuid.UUID, name string) (eventUseCase.LiveScreenSummaryView, error) {
	id, screen, err := u.screen(screenID)
	if err != nil {
		return eventUseCase.LiveScreenSummaryView{}, err
	}
	normalized, err := eventContentModel.NormalizeLiveDisplayName(name)
	if err != nil {
		return eventUseCase.LiveScreenSummaryView{}, err
	}
	screen.name = normalized
	return u.summary(id), nil
}

func (u *liveContractUC) ReorderLiveScreens(ctx context.Context, eventID uuid.UUID, screenIDs []uuid.UUID) ([]eventUseCase.LiveScreenSummaryView, error) {
	u.order = screenIDs
	return u.ListLiveScreens(ctx, eventID, false)
}

func (u *liveContractUC) DeleteLiveScreen(_ context.Context, _, screenID uuid.UUID) error {
	if len(u.order) == 1 {
		return eventContentModel.ErrLiveScreenLast.Err()
	}
	delete(u.screens, screenID)
	order := u.order[:0]
	for _, id := range u.order {
		if id != screenID {
			order = append(order, id)
		}
	}
	u.order = order
	return nil
}

func (u *liveContractUC) SaveLiveLayoutDraft(_ context.Context, _, screenID uuid.UUID, layout eventContentModel.LiveLayout) error {
	_, screen, err := u.screen(screenID)
	if err != nil {
		return err
	}
	screen.draft = &layout
	return nil
}

func (u *liveContractUC) PublishLiveLayout(_ context.Context, _, screenID uuid.UUID) (eventContentModel.LiveLayout, error) {
	_, screen, err := u.screen(screenID)
	if err != nil {
		return eventContentModel.LiveLayout{}, err
	}
	next := *screen.draft
	next.Version = screen.published.Version + 1
	screen.published, screen.draft = next, nil
	return screen.published, nil
}

func (u *liveContractUC) GetLiveScreenLink(_ context.Context, _, screenID uuid.UUID) (*eventUseCase.LiveScreenLinkView, error) {
	_, screen, err := u.screen(screenID)
	if err != nil {
		return nil, err
	}
	return screen.link, nil
}

func (u *liveContractUC) IssueLiveScreenLink(_ context.Context, _, screenID, _ uuid.UUID, _ eventContentModel.LiveScreenExpiry) (eventUseCase.IssuedLiveScreenLinkView, error) {
	_, screen, err := u.screen(screenID)
	if err != nil {
		return eventUseCase.IssuedLiveScreenLinkView{}, err
	}
	screen.link = &eventUseCase.LiveScreenLinkView{ID: uuid.Must(uuid.NewV7()), CreatedAt: time.Now().UTC()}
	return eventUseCase.IssuedLiveScreenLinkView{LiveScreenLinkView: *screen.link, Token: "token-" + screen.name}, nil
}

func (u *liveContractUC) RevokeLiveScreenLink(_ context.Context, _, screenID uuid.UUID) error {
	_, screen, err := u.screen(screenID)
	if err != nil {
		return err
	}
	screen.link = nil
	return nil
}

func liveStatusRequest(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	write := httptest.NewRecorder()
	router.ServeHTTP(write, request)
	return write
}

func layoutBody(t *testing.T, layout eventContentModel.LiveLayout) string {
	t.Helper()
	body, err := json.Marshal(struct {
		Layout eventContentModel.LiveLayout `json:"Layout"`
	}{Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

type liveEditorPayload struct {
	ID        uuid.UUID
	Name      string
	Version   int64
	HasDraft  bool
	Published eventContentModel.LiveLayout
	Draft     *eventContentModel.LiveLayout
}

// The single-screen routes keep their shape (Published, Draft, Version) and
// address the first screen.
func TestLiveLayoutHTTPContractForEditor(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := newLiveContractUC()
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/content/live"

	read := eventRequest(t, router, http.MethodGet, base, "")
	var initial liveEditorPayload
	if err := json.Unmarshal(read["Data"], &initial); err != nil || initial.Published.Version != 1 || initial.Draft != nil || initial.ID != u.order[0] || initial.Name != "Екран 1" {
		t.Fatalf("initial editor payload: %s error=%v", read["Data"], err)
	}

	draft := eventContentModel.DefaultLiveLayout()
	draft.Theme = "light"
	if write := liveStatusRequest(t, router, http.MethodPut, base, layoutBody(t, draft)); write.Code != http.StatusNoContent || u.screens[u.order[0]].draft == nil || u.screens[u.order[0]].draft.Theme != "light" {
		t.Fatalf("save draft: status=%d body=%s", write.Code, write.Body.String())
	}

	read = eventRequest(t, router, http.MethodGet, base, "")
	var pending liveEditorPayload
	if err := json.Unmarshal(read["Data"], &pending); err != nil || pending.Draft == nil || pending.Published.Theme != "dark" || !pending.HasDraft {
		t.Fatalf("pending editor payload: %s error=%v", read["Data"], err)
	}

	result := eventRequest(t, router, http.MethodPost, base+"/publish", "")
	var published eventContentModel.LiveLayout
	if err := json.Unmarshal(result["Data"], &published); err != nil || published.Version != 2 || published.Theme != "light" {
		t.Fatalf("publish payload: %s error=%v", result["Data"], err)
	}

	// Open live screens poll the version and reload the layout on a change.
	version := eventRequest(t, router, http.MethodGet, base+"/version", "")
	if string(version["Data"]) != `{"Version":2,"ScreenID":"`+u.order[0].String()+`"}` {
		t.Fatalf("version payload: %s", version["Data"])
	}
}

func TestLiveScreensHTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := newLiveContractUC()
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/content/live"
	first := u.order[0]

	// A new screen copies the first one under its own name.
	write := liveStatusRequest(t, router, http.MethodPost, base+"/screens", `{"Name":"Фойє","CopyFrom":"`+first.String()+`"}`)
	if write.Code != http.StatusCreated || u.created.Name != "Фойє" || u.created.CopyFrom == nil || *u.created.CopyFrom != first || u.created.Layout != nil {
		t.Fatalf("create: status=%d body=%s input=%+v", write.Code, write.Body.String(), u.created)
	}
	second := u.order[1]
	// A template layout comes in as Layout.
	template := eventContentModel.DefaultLiveLayout()
	template.Theme = "light"
	templateBody, _ := json.Marshal(map[string]any{"Layout": template})
	if write = liveStatusRequest(t, router, http.MethodPost, base+"/screens", string(templateBody)); write.Code != http.StatusCreated || u.created.Layout == nil || u.created.Layout.Theme != "light" || u.created.Name != "" {
		t.Fatalf("create from template: status=%d input=%+v", write.Code, u.created)
	}
	third := u.order[2]

	list := eventRequest(t, router, http.MethodGet, base+"/screens", "")
	var screens struct {
		Screens []struct {
			ID       uuid.UUID
			Name     string
			Position int32
			Version  int64
			HasDraft bool
			Link     *struct{ ID uuid.UUID }
		}
		Max          int
		LinksVisible bool
	}
	if err := json.Unmarshal(list["Data"], &screens); err != nil || len(screens.Screens) != 3 || screens.Max != 10 || !screens.LinksVisible || !u.withLink ||
		screens.Screens[1].ID != second || screens.Screens[1].Name != "Фойє" || screens.Screens[1].Position != 1 {
		t.Fatalf("list: %s error=%v", list["Data"], err)
	}

	// Draft, publish and version per screen; the first screen stays as it was.
	draft := eventContentModel.DefaultLiveLayout()
	draft.Aspect = "4:3"
	if write = liveStatusRequest(t, router, http.MethodPut, base+"/screens/"+second.String()+"/draft", layoutBody(t, draft)); write.Code != http.StatusNoContent {
		t.Fatalf("save screen draft: %d %s", write.Code, write.Body.String())
	}
	editor := eventRequest(t, router, http.MethodGet, base+"/screens/"+second.String(), "")
	var payload liveEditorPayload
	if err := json.Unmarshal(editor["Data"], &payload); err != nil || payload.ID != second || payload.Draft == nil || payload.Draft.Aspect != "4:3" || u.screens[first].draft != nil {
		t.Fatalf("screen editor: %s error=%v", editor["Data"], err)
	}
	eventRequest(t, router, http.MethodPost, base+"/screens/"+second.String()+"/publish", "")
	if version := eventRequest(t, router, http.MethodGet, base+"/screens/"+second.String()+"/version", ""); string(version["Data"]) != `{"Version":2,"ScreenID":"`+second.String()+`"}` {
		t.Fatalf("screen version: %s", version["Data"])
	}
	// The moderator's /live page picks a screen with ?screen.
	if version := eventRequest(t, router, http.MethodGet, base+"/version?screen="+second.String(), ""); string(version["Data"]) != `{"Version":2,"ScreenID":"`+second.String()+`"}` {
		t.Fatalf("version by ?screen: %s", version["Data"])
	}
	read := eventRequest(t, router, http.MethodGet, base+"?screen="+second.String(), "")
	if err := json.Unmarshal(read["Data"], &payload); err != nil || payload.ID != second || payload.Published.Aspect != "4:3" {
		t.Fatalf("editor by ?screen: %s error=%v", read["Data"], err)
	}
	if write = liveStatusRequest(t, router, http.MethodGet, base+"?screen=nope", ""); write.Code != http.StatusBadRequest {
		t.Fatalf("a malformed ?screen: %d", write.Code)
	}
	if write = liveStatusRequest(t, router, http.MethodGet, base+"?screen="+uuid.Must(uuid.NewV7()).String(), ""); write.Code != http.StatusNotFound {
		t.Fatalf("an unknown ?screen: %d %s", write.Code, write.Body.String())
	}

	// Rename, duplicate (no body), reorder.
	renamed := eventRequest(t, router, http.MethodPatch, base+"/screens/"+second.String(), `{"Name":"  Хол  "}`)
	if !strings.Contains(string(renamed["Data"]), `"Name":"Хол"`) {
		t.Fatalf("rename: %s", renamed["Data"])
	}
	if write = liveStatusRequest(t, router, http.MethodPatch, base+"/screens/"+second.String(), `{"Name":""}`); write.Code != http.StatusBadRequest {
		t.Fatalf("an empty name: %d %s", write.Code, write.Body.String())
	}
	if write = liveStatusRequest(t, router, http.MethodPost, base+"/screens/"+second.String()+"/duplicate", ""); write.Code != http.StatusCreated || u.created.CopyFrom == nil || *u.created.CopyFrom != second {
		t.Fatalf("duplicate: %d %s", write.Code, write.Body.String())
	}
	fourth := u.order[3]
	order := eventRequest(t, router, http.MethodPut, base+"/screen-order", `{"ScreenIDs":["`+fourth.String()+`","`+first.String()+`","`+third.String()+`","`+second.String()+`"]}`)
	if u.order[0] != fourth || !strings.Contains(string(order["Data"]), `"Max":10`) {
		t.Fatalf("reorder: %s order=%v", order["Data"], u.order)
	}

	// Links per screen; the single-screen link routes are the first screen's.
	issued := eventRequest(t, router, http.MethodPost, base+"/screens/"+second.String()+"/link", `{"Expiry":"none"}`)
	if !strings.Contains(string(issued["Data"]), `"Token":"token-Хол"`) || u.screens[second].link == nil {
		t.Fatalf("issue screen link: %s", issued["Data"])
	}
	if state := eventRequest(t, router, http.MethodGet, base+"/screens/"+second.String()+"/link", ""); strings.Contains(string(state["Data"]), "Token") || strings.Contains(string(state["Data"]), `"Link":null`) {
		t.Fatalf("screen link state: %s", state["Data"])
	}
	if state := eventRequest(t, router, http.MethodGet, base+"/screen-link", ""); string(state["Data"]) != `{"Link":null}` {
		t.Fatalf("the first screen (now «Екран 2») has no link: %s", state["Data"])
	}
	eventRequest(t, router, http.MethodPost, base+"/screen-link", `{"Expiry":"none"}`)
	if u.screens[fourth].link == nil {
		t.Fatal("the single-screen link route must issue the first screen's link")
	}
	if write = liveStatusRequest(t, router, http.MethodDelete, base+"/screens/"+second.String()+"/link", ""); write.Code != http.StatusNoContent || u.screens[second].link != nil {
		t.Fatalf("revoke screen link: %d", write.Code)
	}

	// Delete; never the last screen.
	for _, id := range []uuid.UUID{second, third, fourth} {
		if write = liveStatusRequest(t, router, http.MethodDelete, base+"/screens/"+id.String(), ""); write.Code != http.StatusNoContent {
			t.Fatalf("delete: %d %s", write.Code, write.Body.String())
		}
	}
	if write = liveStatusRequest(t, router, http.MethodDelete, base+"/screens/"+first.String(), ""); write.Code != http.StatusConflict {
		t.Fatalf("deleting the last screen: %d %s", write.Code, write.Body.String())
	}
	if write = liveStatusRequest(t, router, http.MethodGet, base+"/screens/not-a-uuid", ""); write.Code != http.StatusBadRequest {
		t.Fatalf("a malformed screen ID: %d", write.Code)
	}
}
