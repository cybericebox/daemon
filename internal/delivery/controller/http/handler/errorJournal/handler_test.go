package errorJournal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	errorJournalModel "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/model/rbac"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

type fakeUseCase struct {
	filter   errorJournalUseCase.GroupFilter
	groups   []errorJournalModel.Group
	notFound []errorJournalModel.NotFoundDay
	saved    errorJournalUseCase.SettingsInput
	status   errorJournalModel.Status
	events   chan errorJournalUseCase.StreamEvent
}

func (f *fakeUseCase) ListErrorGroups(_ context.Context, fl errorJournalUseCase.GroupFilter) ([]errorJournalModel.Group, int64, error) {
	f.filter = fl
	return f.groups, int64(len(f.groups)), nil
}
func (f *fakeUseCase) GetErrorGroup(_ context.Context, id uuid.UUID) (errorJournalUseCase.GroupDetail, error) {
	if len(f.groups) == 0 || f.groups[0].ID != id {
		return errorJournalUseCase.GroupDetail{}, errorJournalModel.ErrGroupNotFound.Err()
	}
	return errorJournalUseCase.GroupDetail{Group: f.groups[0], Samples: []errorJournalModel.Sample{{ID: uuid.Must(uuid.NewV7()), Message: "m"}}}, nil
}
func (f *fakeUseCase) SetErrorGroupStatus(_ context.Context, id uuid.UUID, s errorJournalModel.Status) (errorJournalModel.Group, error) {
	if !s.Valid() {
		return errorJournalModel.Group{}, errorJournalModel.ErrStatusInvalid.Err()
	}
	f.status = s
	g := f.groups[0]
	g.Status = s
	return g, nil
}
func (f *fakeUseCase) ErrorNotFoundStats(context.Context, time.Time, time.Time) ([]errorJournalModel.NotFoundDay, error) {
	return f.notFound, nil
}
func (f *fakeUseCase) GetErrorJournalSettings(context.Context) (errorJournalUseCase.SettingsView, error) {
	return errorJournalUseCase.SettingsView{TelegramEnabled: true, Settings: errorJournalModel.Settings{
		Emails: []string{"a@example.org"}, TelegramChats: []errorJournalModel.TelegramChat{{ChatID: "1", Failing: true, LastError: "blocked"}},
	}}, nil
}
func (f *fakeUseCase) SaveErrorJournalSettings(ctx context.Context, in errorJournalUseCase.SettingsInput) (errorJournalUseCase.SettingsView, error) {
	f.saved = in
	return f.GetErrorJournalSettings(ctx)
}
func (f *fakeUseCase) SendErrorJournalTest(context.Context) ([]errorJournalUseCase.TestResult, error) {
	return []errorJournalUseCase.TestResult{{Channel: "telegram", Target: "1", OK: false, Error: "blocked"}}, nil
}
func (f *fakeUseCase) ErrorJournalStream() (<-chan errorJournalUseCase.StreamEvent, func()) {
	return f.events, func() {}
}

// gateRecorder remembers which permission each route asked for.
type gateRecorder struct{ hit []rbac.Permission }

func (g *gateRecorder) RequirePermission(p rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		g.hit = append(g.hit, p)
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleSuperAdmin}))
	}
}

func setup(t *testing.T) (*gin.Engine, *fakeUseCase, *gateRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	uc := &fakeUseCase{events: make(chan errorJournalUseCase.StreamEvent, 4)}
	gate := &gateRecorder{}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	New(uc, gate).Init(router.Group("api"))
	return router, uc, gate
}

func call(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func data(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct{ Data map[string]any }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env), w.Body.String())
	return env.Data
}

func TestEveryRouteIsGatedAndWritesNeedTheWritePermission(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	routes := []struct {
		method, path string
		want         rbac.Permission
	}{
		{http.MethodGet, "/api/admin/errors", rbac.PermPlatformErrorsRead},
		{http.MethodGet, "/api/admin/errors/" + id, rbac.PermPlatformErrorsRead},
		{http.MethodGet, "/api/admin/errors/not-found", rbac.PermPlatformErrorsRead},
		{http.MethodGet, "/api/admin/errors/settings", rbac.PermPlatformErrorsRead},
		{http.MethodPut, "/api/admin/errors/settings", rbac.PermPlatformErrorsWrite},
		{http.MethodPost, "/api/admin/errors/settings/test", rbac.PermPlatformErrorsWrite},
		{http.MethodPatch, "/api/admin/errors/" + id + "/status", rbac.PermPlatformErrorsWrite},
	}
	for _, rt := range routes {
		r, _, gate := setup(t)
		call(r, rt.method, rt.path, "{}")
		require.Equal(t, []rbac.Permission{rt.want}, gate.hit, rt.method+" "+rt.path)
	}
	// nobody holds them except through "*", which only a super admin has
	for _, role := range []rbac.Role{rbac.RoleAdmin, rbac.RoleAdminViewer, rbac.RoleUser, rbac.RolePublic} {
		assert.False(t, role.HasPermission(rbac.PermPlatformErrorsRead), role)
		assert.False(t, role.HasPermission(rbac.PermPlatformErrorsWrite), role)
	}
	assert.True(t, rbac.RoleSuperAdmin.HasPermission(rbac.PermPlatformErrorsWrite))
}

func TestListParsesFiltersAndReturnsGroups(t *testing.T) {
	r, uc, _ := setup(t)
	uc.groups = []errorJournalModel.Group{{ID: uuid.Must(uuid.NewV7()), Kind: errorJournalModel.KindPanic, Title: "t", Status: errorJournalModel.StatusOpen, Occurrences: 3}}

	w := call(r, http.MethodGet, "/api/admin/errors?kind=panic,http_403&kind=job&status=open&q=boom&request=0198C1F2&from=2026-10-01T00:00:00Z&limit=20&offset=40", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []errorJournalModel.Kind{"panic", "http_403", "job"}, uc.filter.Kinds)
	assert.Equal(t, errorJournalModel.StatusOpen, uc.filter.Status)
	assert.Equal(t, "boom", uc.filter.Query)
	assert.Equal(t, "0198C1F2", uc.filter.Request)
	assert.Equal(t, 20, uc.filter.Limit)
	assert.Equal(t, 40, uc.filter.Offset)
	require.NotNil(t, uc.filter.From)
	assert.Nil(t, uc.filter.To)
	body := data(t, w)
	assert.EqualValues(t, 1, body["Total"])
	item := body["Items"].([]any)[0].(map[string]any)
	assert.Equal(t, "panic", item["Kind"])
	assert.EqualValues(t, 3, item["Occurrences"])
	_, hasIP := item["IP"]
	assert.False(t, hasIP)

	assert.Equal(t, 400, call(r, http.MethodGet, "/api/admin/errors?from=yesterday", "").Code)
	assert.Equal(t, 400, call(r, http.MethodGet, "/api/admin/errors?limit=abc", "").Code)
}

func TestDetailShowsSamplesAndMissingGroupIs404(t *testing.T) {
	r, uc, _ := setup(t)
	id := uuid.Must(uuid.NewV7())
	uc.groups = []errorJournalModel.Group{{ID: id}}
	w := call(r, http.MethodGet, "/api/admin/errors/"+id.String(), "")
	require.Equal(t, 200, w.Code)
	assert.Len(t, data(t, w)["Samples"], 1)
	assert.Equal(t, 404, call(r, http.MethodGet, "/api/admin/errors/"+uuid.Must(uuid.NewV7()).String(), "").Code)
	assert.Equal(t, 400, call(r, http.MethodGet, "/api/admin/errors/not-a-uuid", "").Code)
}

func TestSetStatus(t *testing.T) {
	r, uc, _ := setup(t)
	id := uuid.Must(uuid.NewV7())
	uc.groups = []errorJournalModel.Group{{ID: id}}
	w := call(r, http.MethodPatch, "/api/admin/errors/"+id.String()+"/status", `{"Status":"resolved"}`)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, errorJournalModel.StatusResolved, uc.status)
	assert.Equal(t, 400, call(r, http.MethodPatch, "/api/admin/errors/"+id.String()+"/status", `{"Status":"bogus"}`).Code)
	assert.Equal(t, 400, call(r, http.MethodPatch, "/api/admin/errors/"+id.String()+"/status", `{}`).Code)
}

func TestNotFoundStatsSumPerRouteWithoutPaths(t *testing.T) {
	r, uc, _ := setup(t)
	d1, d2 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	uc.notFound = []errorJournalModel.NotFoundDay{{Day: d2, Route: "", Hits: 40}, {Day: d2, Route: "/api/e/:id", Hits: 2}, {Day: d1, Route: "", Hits: 10}}

	w := call(r, http.MethodGet, "/api/admin/errors/not-found", "")
	require.Equal(t, 200, w.Code)
	body := data(t, w)
	assert.EqualValues(t, 52, body["Total"])
	routes := body["Routes"].([]any)
	first := routes[0].(map[string]any)
	assert.Equal(t, "", first["Route"], "the unmatched counter has an empty route")
	assert.EqualValues(t, 50, first["Hits"])
	day := body["Days"].([]any)[0].(map[string]any)
	assert.Equal(t, "2026-10-02", day["Day"])
}

func TestSettingsRoundTripAndTest(t *testing.T) {
	r, uc, _ := setup(t)
	w := call(r, http.MethodGet, "/api/admin/errors/settings", "")
	require.Equal(t, 200, w.Code)
	body := data(t, w)
	assert.Equal(t, true, body["TelegramEnabled"])
	chat := body["TelegramChats"].([]any)[0].(map[string]any)
	assert.Equal(t, true, chat["Failing"])

	w = call(r, http.MethodPut, "/api/admin/errors/settings", `{"Emails":["a@example.org"],"EmailToSuperAdmins":true,"TelegramChats":[{"ChatID":"-100","Label":"ops"}]}`)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"a@example.org"}, uc.saved.Emails)
	assert.True(t, uc.saved.EmailToSuperAdmins)
	assert.Equal(t, []errorJournalUseCase.ChatInput{{ChatID: "-100", Label: "ops"}}, uc.saved.TelegramChats)

	w = call(r, http.MethodPost, "/api/admin/errors/settings/test", "")
	require.Equal(t, 200, w.Code)
	result := data(t, w)["Results"].([]any)[0].(map[string]any)
	assert.Equal(t, "telegram", result["Channel"])
	assert.Equal(t, false, result["OK"])
}

func TestStreamSendsErrorGroupEventsAndHeartbeats(t *testing.T) {
	r, uc, _ := setup(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/admin/errors/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	sample := errorJournalModel.Sample{ID: uuid.Must(uuid.NewV7()), Message: "boom", RequestID: "req-1"}
	uc.events <- errorJournalUseCase.StreamEvent{Group: errorJournalModel.Group{ID: uuid.Must(uuid.NewV7()), Kind: errorJournalModel.KindHTTP5xx}, Sample: &sample, New: true}

	scanner := bufio.NewScanner(resp.Body)
	var event, payload string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			payload = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	assert.Equal(t, "error-group", event)
	var ev streamEvent
	require.NoError(t, json.NewDecoder(bytes.NewBufferString(payload)).Decode(&ev))
	assert.True(t, ev.New)
	assert.Equal(t, "http_5xx", ev.Group.Kind)
	require.NotNil(t, ev.Sample)
	assert.Equal(t, "req-1", ev.Sample.RequestID)
}
