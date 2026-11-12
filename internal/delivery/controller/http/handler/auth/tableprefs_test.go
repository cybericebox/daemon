package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tablePrefsModel "github.com/cybericebox/daemon/internal/model/tablePrefs"
)

func TestTablePrefs_RoutesNeedASession(t *testing.T) {
	r := passkeyRouter(&fakeUC{}, &fakeProt{}, false)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		r.ServeHTTP(
			w,
			httptest.NewRequest(
				m,
				"/api/auth/account/table-prefs/admin.users",
				strings.NewReader(`{}`),
			),
		)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: want 401, got %d", m, w.Code)
		}
	}
}

func TestTablePrefs_GetSaveReset(t *testing.T) {
	uc := &fakeUC{tablePrefs: &tablePrefsModel.Prefs{ColumnOrder: []string{"name"}, PageSize: 50}}
	r := passkeyRouter(uc, &fakeProt{}, true)

	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(http.MethodGet, "/api/auth/account/table-prefs/admin.users", nil),
	)
	var env struct {
		Data tablePrefsModel.Prefs `json:"data"`
	}
	if err := json.Unmarshal(
		w.Body.Bytes(),
		&env,
	); err != nil || w.Code != http.StatusOK || env.Data.PageSize != 50 ||
		uc.tableKey != "admin.users" {
		t.Fatalf("get: %d %s (%v)", w.Code, w.Body, err)
	}

	body := `{"ColumnOrder":["a","b"],"ColumnWidths":{"a":120},"Hidden":["c"],"Sort":{"Key":"a","Dir":"desc"},"PageSize":25}`
	w = httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPut,
			"/api/auth/account/table-prefs/event.teams",
			strings.NewReader(body),
		),
	)
	if err := json.Unmarshal(
		w.Body.Bytes(),
		&env,
	); err != nil || w.Code != http.StatusOK || env.Data.Sort == nil || env.Data.Sort.Dir != "desc" ||
		uc.tableKey != "event.teams" {
		t.Fatalf("put: %d %s (%v)", w.Code, w.Body, err)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(http.MethodDelete, "/api/auth/account/table-prefs/event.teams", nil),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
}
