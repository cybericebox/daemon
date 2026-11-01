package infrastructure

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	infrastructureUseCase "github.com/cybericebox/daemon/internal/useCase/infrastructure"
)

type bulkBody struct {
	Data struct {
		Results []struct {
			EventID, TeamID, ID uuid.UUID
			Ok                  bool
			Code                string
		}
	} `json:"data"`
}

func TestBulkRecreateReportsEachStandAndAuditsEachOne(t *testing.T) {
	event, good, bad := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{recreateFailsFor: bad}
	prot := newProtection()
	router := newRouter(uc, prot)

	body := `{"Items":[{"EventID":"` + event.String() + `","TeamID":"` + good.String() + `"},{"EventID":"` + event.String() + `","TeamID":"` + bad.String() + `"}]}`
	w := httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/infrastructure/stands/bulk/recreate",
			strings.NewReader(body),
		),
	)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var out bulkBody
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	res := out.Data.Results
	if len(res) != 2 || !res[0].Ok || res[0].TeamID != good || res[1].Ok || res[1].Code == "" {
		t.Fatalf("results = %+v", res)
	}
	if len(uc.audited) != 2 ||
		uc.audited[0].Target != "event:"+event.String()+" team:"+good.String() ||
		uc.audited[1].ResponseStatus != http.StatusNotFound {
		t.Fatalf("audit = %+v", uc.audited)
	}
	if prot.gates["POST /api/infrastructure/stands/bulk/recreate"] == "" {
		t.Logf("gates: %v", prot.gates)
	}
}

func TestBulkRecreateRefusesAnEmptyOrOversizedRequest(t *testing.T) {
	router := newRouter(&fakeUseCase{}, newProtection())
	items := make([]string, 51)
	for i := range items {
		items[i] = `{"EventID":"` + uuid.Must(uuid.NewV7()).
			String() +
			`","TeamID":"` + uuid.Must(uuid.NewV7()).
			String() +
			`"}`
	}
	for _, body := range []string{`{"Items":[]}`, `{"Items":[` + strings.Join(items, ",") + `]}`} {
		w := httptest.NewRecorder()
		router.ServeHTTP(
			w,
			httptest.NewRequest(
				http.MethodPost,
				"/api/infrastructure/stands/bulk/recreate",
				strings.NewReader(body),
			),
		)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d for %.40s", w.Code, body)
		}
	}
}

func TestBulkTerminateTestLabsKeepsTheOrderAndAuditsEach(t *testing.T) {
	ok, missing := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{
		bulkLabResults: []infrastructureUseCase.BulkTestLabResult{
			{ID: ok},
			{ID: missing, Err: infraModel.ErrTestLabNotFound.Err()},
		},
	}
	router := newRouter(uc, newProtection())

	w := httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/infrastructure/test-labs/bulk/terminate",
			strings.NewReader(`{"IDs":["`+ok.String()+`","`+missing.String()+`"]}`),
		),
	)

	var out bulkBody
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status = %d err = %v body = %s", w.Code, err, w.Body.String())
	}
	res := out.Data.Results
	if len(res) != 2 || !res[0].Ok || res[1].Ok || res[1].Code == "" || res[1].ID != missing {
		t.Fatalf("results = %+v", res)
	}
	if len(uc.audited) != 2 || uc.audited[1].Target != "test-lab:"+missing.String() {
		t.Fatalf("audit = %+v", uc.audited)
	}
}

func TestListFiltersAndSortingAreValidated(t *testing.T) {
	uc := &fakeUseCase{}
	router := newRouter(uc, newProtection())
	for _, target := range []string{
		"/api/infrastructure/stands?page=1&pageSize=10&sortBy=password",
		"/api/infrastructure/stands?page=1&pageSize=10&sortBy=team&sortDir=sideways",
		"/api/infrastructure/test-labs?page=1&pageSize=10&status=maybe",
		"/api/infrastructure/test-labs?page=1&pageSize=10&author=nobody",
		"/api/infrastructure/test-labs?page=1&pageSize=10&sortBy=owner",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", target, w.Code)
		}
	}
	author := uuid.Must(uuid.NewV7())
	w := httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodGet,
			"/api/infrastructure/test-labs?page=1&pageSize=10&status=expired&sortBy=author&sortDir=asc&author="+author.String(),
			nil,
		),
	)
	if w.Code != http.StatusOK || uc.testLabsFilter.Status != "expired" ||
		uc.testLabsFilter.SortBy != "author" ||
		uc.testLabsFilter.AuthorID.UUID != author {
		t.Fatalf("status = %d filter = %+v", w.Code, uc.testLabsFilter)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodGet,
			"/api/infrastructure/stands?page=1&pageSize=10&sortBy=team&sortDir=desc",
			nil,
		),
	)
	if w.Code != http.StatusOK || uc.standsFilter.SortBy != "team" ||
		uc.standsFilter.SortDir != "desc" {
		t.Fatalf("status = %d filter = %+v", w.Code, uc.standsFilter)
	}

	for _, path := range []string{
		"/api/infrastructure/stands?page=1&pageSize=10&sortBy=reason",
		"/api/infrastructure/stands?page=1&pageSize=10&sortBy=cpu&sortDir=desc",
		"/api/infrastructure/stands?page=1&pageSize=10&sortBy=memory",
		"/api/infrastructure/test-labs?page=1&pageSize=10&sortBy=variant",
		"/api/infrastructure/test-labs?page=1&pageSize=10&sortBy=status&sortDir=asc",
	} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, w.Code)
		}
	}
}

// The facets read the same filters as the lists, never the sorting, and refuse a field the table has no facet for.
func TestFacetsReadTheListFilters(t *testing.T) {
	uc := &fakeUseCase{}
	router := newRouter(uc, newProtection())
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	w := get("/api/infrastructure/stands/facets?field=kind&status=failed&search=ctf&sortBy=team&page=3")
	if w.Code != http.StatusOK || uc.facetField != "kind" || len(uc.standsFilter.Statuses) != 1 ||
		uc.standsFilter.Search != "ctf" || uc.standsFilter.SortBy != "" || !strings.Contains(w.Body.String(), `"Counts":{"failed":2}`) {
		t.Fatalf("stand facets: %d %s %+v", w.Code, w.Body.String(), uc.standsFilter)
	}
	author := uuid.Must(uuid.NewV7())
	w = get("/api/infrastructure/test-labs/facets?field=status&author=" + author.String() + "&status=active&sortBy=author")
	if w.Code != http.StatusOK || uc.facetField != "status" || uc.testLabsFilter.AuthorID.UUID != author ||
		uc.testLabsFilter.SortBy != "" || !strings.Contains(w.Body.String(), `"Counts":{"active":3}`) {
		t.Fatalf("test lab facets: %d %s %+v", w.Code, w.Body.String(), uc.testLabsFilter)
	}
	for _, path := range []string{
		"/api/infrastructure/stands/facets?field=bogus",
		"/api/infrastructure/stands/facets?field=kind&status=nope",
		"/api/infrastructure/test-labs/facets?field=bogus",
		"/api/infrastructure/test-labs/facets?field=status&author=x",
	} {
		if w = get(path); w.Code != http.StatusBadRequest {
			t.Fatalf("%s must be refused: %d", path, w.Code)
		}
	}
}
