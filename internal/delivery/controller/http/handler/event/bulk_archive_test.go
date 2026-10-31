package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type bulkArchiveUC struct {
	eventCRUDContractUC

	failing uuid.UUID
	audited []adminAuditUseCase.Entry
}

func (u *bulkArchiveUC) ArchiveEvent(
	_ context.Context,
	id, _ uuid.UUID,
) (eventUseCase.EventView, error) {
	if id == u.failing {
		return eventUseCase.EventView{}, eventModel.ErrEventNotFound.Err()
	}
	u.archiveCalls++
	return u.view, nil
}

func (u *bulkArchiveUC) RecordAdminAction(_ context.Context, entry adminAuditUseCase.Entry) error {
	u.audited = append(u.audited, entry)
	return nil
}

func TestBulkArchiveReportsEachEventAndAuditsEachOne(t *testing.T) {
	actor, ok, missing := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &bulkArchiveUC{failing: missing}
	router := testEventCRUDRouterFor(u, actor)

	body := `{"EventIDs":["` + ok.String() + `","` + missing.String() + `","` + ok.String() + `"]}`
	w := httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(http.MethodPost, "/api/events/bulk/archive", strings.NewReader(body)),
	)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Data struct {
			Results []struct {
				EventID uuid.UUID
				Ok      bool
				Code    string
			}
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	res := out.Data.Results
	if len(res) != 2 || !res[0].Ok || res[1].Ok || res[1].Code == "" || res[0].EventID != ok ||
		res[1].EventID != missing {
		t.Fatalf("results = %+v", res)
	}
	if len(u.audited) != 2 || u.audited[0].Target != "event:"+ok.String() ||
		u.audited[1].ResponseStatus != http.StatusNotFound {
		t.Fatalf("audit = %+v", u.audited)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/events/bulk/archive",
			strings.NewReader(`{"EventIDs":[]}`),
		),
	)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("an empty request: status = %d", w.Code)
	}
}

func TestEventListFiltersAreValidated(t *testing.T) {
	actor := uuid.Must(uuid.NewV7())
	u := &eventCRUDContractUC{}
	router := testEventCRUDRouter(u, actor)
	for _, query := range []string{"startFrom=yesterday", "organizer=nobody", "infrastructure=maybe", "startFrom=2026-10-02T00:00:00Z&startTo=2026-10-01T00:00:00Z"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodGet, "/api/events?page=1&pageSize=10&"+query, nil),
		)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", query, w.Code)
		}
	}
	org := uuid.Must(uuid.NewV7())
	eventRequest(
		t,
		router,
		http.MethodGet,
		"/api/events?page=1&pageSize=10&infrastructure=true&organizer="+org.String()+"&startFrom=2026-10-01T00:00:00Z&startTo=2026-11-01T00:00:00Z",
		"",
	)
	if u.listFilter.Infrastructure != "true" || u.listFilter.OrganizerID != org ||
		u.listFilter.StartFrom == nil ||
		u.listFilter.StartTo == nil {
		t.Fatalf("filter = %+v", u.listFilter)
	}
}

func (u *eventCRUDContractUC) EventFacets(_ context.Context, field string, f eventUseCase.ListEventsFilter) (map[string]int64, error) {
	u.facetField, u.listFilter = field, f
	return map[string]int64{"true": 2}, nil
}

func (u *eventCRUDContractUC) ListEventOrganizers(context.Context) ([]eventRepo.Organizer, error) {
	return []eventRepo.Organizer{{UserID: uuid.Nil, Name: "Ann", Email: "ann@example.test"}}, nil
}

func TestEventFacetsAndOrganizers(t *testing.T) {
	u := &eventCRUDContractUC{}
	router := testEventCRUDRouter(u, uuid.Must(uuid.NewV7()))

	got := eventRequest(t, router, http.MethodGet, "/api/events/facets?field=infrastructure&status=published&startFrom=2026-10-01T00:00:00Z", "")
	if u.facetField != "infrastructure" || u.listFilter.Status != "published" || u.listFilter.StartFrom == nil || !strings.Contains(string(got["Data"]), `"true":2`) {
		t.Fatalf("field = %q filter = %+v data = %s", u.facetField, u.listFilter, got["Data"])
	}
	for _, target := range []string{"/api/events/facets", "/api/events/facets?field=name", "/api/events/facets?field=status&infrastructure=maybe"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", target, w.Code)
		}
	}
	list := eventRequest(t, router, http.MethodGet, "/api/events/organizers", "")
	if !strings.Contains(string(list["Data"]), "ann@example.test") {
		t.Fatalf("organizers = %s", list["Data"])
	}
}
