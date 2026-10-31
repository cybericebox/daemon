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
}
