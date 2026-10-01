package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type eventCRUDContractUC struct {
	eventHandler.IUseCase
	view         eventUseCase.EventView
	createInput  eventUseCase.CreateEventInput
	updateInput  eventUseCase.UpdateEventInput
	listFilter   eventUseCase.ListEventsFilter
	archiveCalls int
	deleteCalls  int
	config       eventUseCase.EventConfigView
	configInput  eventUseCase.UpdateConfigInput
}

func (u *eventCRUDContractUC) GetEventConfig(context.Context, uuid.UUID) (eventUseCase.EventConfigView, error) {
	return u.config, nil
}

func (u *eventCRUDContractUC) UpdateEventConfig(_ context.Context, _ uuid.UUID, in eventUseCase.UpdateConfigInput, _ uuid.UUID) (eventUseCase.EventConfigView, error) {
	u.configInput = in
	return u.config, nil
}

func (u *eventCRUDContractUC) CreateEvent(_ context.Context, in eventUseCase.CreateEventInput) (eventUseCase.EventView, error) {
	u.createInput = in
	return u.view, nil
}

func (u *eventCRUDContractUC) GetEvent(_ context.Context, _ uuid.UUID) (eventUseCase.EventView, error) {
	return u.view, nil
}

func (u *eventCRUDContractUC) ListEvents(_ context.Context, filter eventUseCase.ListEventsFilter) (eventUseCase.EventsListResult, error) {
	u.listFilter = filter
	return eventUseCase.EventsListResult{Events: []eventUseCase.EventView{u.view}, Total: 1}, nil
}

func (u *eventCRUDContractUC) UpdateEvent(_ context.Context, _ uuid.UUID, in eventUseCase.UpdateEventInput, _ uuid.UUID) (eventUseCase.EventView, error) {
	u.updateInput = in
	u.view.Name = in.Name
	return u.view, nil
}

func (u *eventCRUDContractUC) ArchiveEvent(_ context.Context, _ uuid.UUID, _ uuid.UUID) (eventUseCase.EventView, error) {
	u.archiveCalls++
	u.view.ArchiveAt = u.view.AvailableFrom.Add(time.Hour)
	u.view.Status = eventModel.EventArchivedStatus
	return u.view, nil
}

func (u *eventCRUDContractUC) DeleteEvent(_ context.Context, _ uuid.UUID) error {
	u.deleteCalls++
	return nil
}

type allowEventCRUD struct{}

func (allowEventCRUD) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Next() }
}

func testEventCRUDRouter(u *eventCRUDContractUC, actor uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	router.Use(func(ctx *gin.Context) {
		claims := rbac.Claims{UserID: actor, Role: rbac.RoleSuperAdmin}
		ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), claims))
		ctx.Next()
	})
	eventHandler.NewEventAPIHandler(u, allowEventCRUD{}).Init(router.Group("api"))
	return router
}

func eventRequest(t *testing.T, router *gin.Engine, method, path, body string) map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return envelope
}

func TestEventCRUDHTTPContractForAdmin(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	u := &eventCRUDContractUC{view: eventUseCase.EventView{
		ID: eventID, Tag: "autumn", Name: "Internal", AvailableFrom: start,
		Status: eventModel.EventPendingStatus, LifecycleStatus: eventModel.LifecycleNotPublished,
		CreatedAt: start, UpdatedAt: start,
	}}
	router := testEventCRUDRouter(u, actor)
	base := "/api/events"
	item := base + "/" + eventID.String()
	input := `{"Tag":"autumn","Name":"Internal","AvailableFrom":"2026-10-01T09:00:00Z","ArchiveAt":null}`

	created := eventRequest(t, router, http.MethodPost, base, input)
	if !u.createInput.ArchiveAt.IsZero() || u.createInput.CreatedBy != actor {
		t.Fatalf("create input lost optional archive or actor: %+v", u.createInput)
	}
	var data struct {
		ArchiveAt       *time.Time `json:"ArchiveAt"`
		Status          string     `json:"Status"`
		LifecycleStatus string     `json:"LifecycleStatus"`
	}
	if err := json.Unmarshal(created["Data"], &data); err != nil {
		t.Fatal(err)
	}
	if data.ArchiveAt != nil || data.Status != "pending" || data.LifecycleStatus != "not_published" {
		t.Fatalf("created event transport mismatch: %+v", data)
	}
	if got := eventRequest(t, router, http.MethodGet, item, ""); len(got["Data"]) == 0 {
		t.Fatal("GET must return the event")
	}
	if got := eventRequest(t, router, http.MethodGet, base, ""); !strings.Contains(string(got["Data"]), `"Items"`) {
		t.Fatalf("list must use cursor-page Items: %s", got["Data"])
	}
	page := eventRequest(t, router, http.MethodGet, base+"?page=2&pageSize=25&sortBy=status&sortDir=asc&status=published", "")
	if u.listFilter.Page != 2 || u.listFilter.PageSize != 25 || u.listFilter.SortBy != "status" ||
		u.listFilter.SortDir != "asc" || u.listFilter.Status != "published" ||
		!strings.Contains(string(page["Data"]), `"Page":2`) {
		t.Fatalf("offset list contract: filter=%+v data=%s", u.listFilter, page["Data"])
	}

	eventRequest(t, router, http.MethodPut, item, `{"Tag":"autumn","Name":"Renamed","AvailableFrom":"2026-10-01T09:00:00Z","ArchiveAt":null}`)
	if u.updateInput.Name != "Renamed" || !u.updateInput.ArchiveAt.IsZero() {
		t.Fatalf("update input mismatch: %+v", u.updateInput)
	}
	archived := eventRequest(t, router, http.MethodPost, item+"/archive", "")
	if u.archiveCalls != 1 || !strings.Contains(string(archived["Data"]), `"Status":"archived"`) {
		t.Fatalf("archive response mismatch: calls=%d data=%s", u.archiveCalls, archived["Data"])
	}
	eventRequest(t, router, http.MethodDelete, item, "")
	if u.deleteCalls != 1 {
		t.Fatalf("delete calls=%d, want 1", u.deleteCalls)
	}
}

func TestConfigHTTPExposesAdminInfrastructureFlagReadOnly(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &eventCRUDContractUC{config: eventUseCase.EventConfigView{EventID: eventID}, view: eventUseCase.EventView{ID: eventID, InfrastructureAllowed: true}}
	router := testEventCRUDRouter(u, actor)
	path := "/api/events/" + eventID.String() + "/config"
	got := eventRequest(t, router, http.MethodGet, path, "")
	if !strings.Contains(string(got["Data"]), `"InfrastructureAllowed":true`) || strings.Contains(string(got["Data"]), "DynamicLabsPlanned") {
		t.Fatalf("GET must expose only the admin flag: %s", got["Data"])
	}
}

func TestCreateEventPassesInfrastructureChoice(t *testing.T) {
	actor := uuid.Must(uuid.NewV7())
	u := &eventCRUDContractUC{view: eventUseCase.EventView{ID: uuid.Must(uuid.NewV7()), Tag: "ctf", Name: "CTF", InfrastructureAllowed: true}}
	router := testEventCRUDRouter(u, actor)
	created := eventRequest(t, router, http.MethodPost, "/api/events", `{"Tag":"ctf","Name":"CTF","AvailableFrom":"2026-10-01T00:00:00Z","InfrastructureAllowed":false}`)
	if u.createInput.InfrastructureAllowed == nil || *u.createInput.InfrastructureAllowed {
		t.Fatalf("explicit choice lost: %+v", u.createInput)
	}
	if !strings.Contains(string(created["Data"]), `"InfrastructureAllowed":true`) {
		t.Fatalf("response must expose the stored flag: %s", created["Data"])
	}
	eventRequest(t, router, http.MethodPost, "/api/events", `{"Tag":"ctf","Name":"CTF","AvailableFrom":"2026-10-01T00:00:00Z"}`)
	if u.createInput.InfrastructureAllowed != nil {
		t.Fatal("an omitted choice must stay nil so the use case applies the default")
	}
}
