package platformAnalytics

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// ouUseCase serves the overview and users routes; any other call panics on
// the nil embedded interface.
type ouUseCase struct {
	IUseCase
	overview   platformAnalyticsUseCase.OverviewView
	users      platformAnalyticsUseCase.UsersView
	people     []platformAnalyticsUseCase.UsersPersonView
	calls      int
	peopleCall int
	from, to   *time.Time
}

func (f *ouUseCase) GetOverview(_ context.Context, from, to *time.Time) (platformAnalyticsUseCase.OverviewView, error) {
	f.calls++
	f.from, f.to = from, to
	return f.overview, nil
}

func (f *ouUseCase) GetUsers(_ context.Context, from, to *time.Time) (platformAnalyticsUseCase.UsersView, error) {
	f.calls++
	f.from, f.to = from, to
	return f.users, nil
}

func (f *ouUseCase) GetUsersPeople(context.Context) ([]platformAnalyticsUseCase.UsersPersonView, error) {
	f.peopleCall++
	return f.people, nil
}

// ouProt holds a set of permissions; a route whose gate is not in the set answers 403.
type ouProt struct{ granted map[rbac.Permission]bool }

func (p *ouProt) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !p.granted[required] {
			ctx.AbortWithStatus(http.StatusForbidden)
		}
	}
}

func ouGrant(perms ...rbac.Permission) *ouProt {
	p := &ouProt{granted: map[rbac.Permission]bool{}}
	for _, perm := range perms {
		p.granted[perm] = true
	}
	return p
}

func ouServe(t *testing.T, uc *ouUseCase, prot *ouProt, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewPlatformAnalyticsAPIHandler(uc, prot).Init(router.Group("api"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func ouRows(t *testing.T, w *httptest.ResponseRecorder) [][]string {
	t.Helper()
	body := w.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Fatalf("CSV must start with a UTF-8 BOM: %q", body)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body[3:]))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestOverviewAndUsersNeedAnalyticsRead(t *testing.T) {
	for _, target := range []string{
		"/api/analytics/overview", "/api/analytics/overview/export.csv?table=summary",
		"/api/analytics/users", "/api/analytics/users/export.csv?table=methods",
	} {
		uc := &ouUseCase{}
		w := ouServe(t, uc, ouGrant(), target)
		if w.Code != http.StatusForbidden || uc.calls != 0 {
			t.Fatalf("%s: status = %d, calls = %d, want 403 and no report", target, w.Code, uc.calls)
		}
		uc = &ouUseCase{}
		if w = ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead), target); w.Code != http.StatusOK || uc.calls != 1 {
			t.Fatalf("%s: status = %d, calls = %d, want 200 and one report", target, w.Code, uc.calls)
		}
	}
}

// The per-user rows are behind analytics.users.read, not analytics.read.
func TestPeopleNeedTheUsersReadPermission(t *testing.T) {
	uc := &ouUseCase{people: []platformAnalyticsUseCase.UsersPersonView{{ID: uuid.Must(uuid.NewV7()), Name: "Ada", Email: "ada@example.test", Role: "user", EventsJoined: 3, Solves: 7}}}
	for _, target := range []string{"/api/analytics/users/people", "/api/analytics/users/export.csv?table=people"} {
		w := ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead), target)
		if w.Code != http.StatusForbidden || uc.peopleCall != 0 {
			t.Fatalf("%s with analytics.read only: status = %d, people calls = %d, want 403 and none", target, w.Code, uc.peopleCall)
		}
	}
	w := ouServe(t, uc, ouGrant(rbac.PermAnalyticsUsersRead), "/api/analytics/users/people")
	if w.Code != http.StatusOK || uc.peopleCall != 1 {
		t.Fatalf("status = %d, people calls = %d", w.Code, uc.peopleCall)
	}
	var body struct {
		Data []struct {
			Name         string
			Email        string
			Role         string
			EventsJoined int64
			Solves       int64
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Name != "Ada" || body.Data[0].EventsJoined != 3 || body.Data[0].Solves != 7 {
		t.Fatalf("body = %s", w.Body.String())
	}
	if w = ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead, rbac.PermAnalyticsUsersRead), "/api/analytics/users/export.csv?table=people"); w.Code != http.StatusOK {
		t.Fatalf("people export status = %d", w.Code)
	}
}

func TestOverviewPassesThePeriodAndMapsTheReport(t *testing.T) {
	prev := int64(10)
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	uc := &ouUseCase{overview: platformAnalyticsUseCase.OverviewView{
		Period: platformAnalyticsUseCase.OverviewPeriodView{From: day.AddDate(0, 0, -7), To: day, Previous: &platformAnalyticsUseCase.OverviewPeriodBoundsView{From: day.AddDate(0, 0, -14), To: day.AddDate(0, 0, -7)}},
		Users:  platformAnalyticsUseCase.OverviewUsersView{Total: 100, New: platformAnalyticsUseCase.OverviewMetricView{Value: 12, Previous: &prev}},
		Events: platformAnalyticsUseCase.OverviewEventsView{Running: 2, Total: 9},
		Series: platformAnalyticsUseCase.OverviewSeriesView{
			NewUsers: []platformAnalyticsUseCase.OverviewDayNewView{{Day: day, New: 3}},
			Activity: []platformAnalyticsUseCase.OverviewDayActivityView{{Day: day, Attempts: 5, Solves: 2}},
		},
	}}
	w := ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead), "/api/analytics/overview?from=2026-09-22T00:00:00Z&to=2026-09-29T00:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if uc.from == nil || !uc.from.Equal(day.AddDate(0, 0, -7)) || uc.to == nil || !uc.to.Equal(day) {
		t.Fatalf("period = %v %v", uc.from, uc.to)
	}
	var body struct {
		Data overviewResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Data
	if d.Users.Total != 100 || d.Users.New.Value != 12 || d.Users.New.Previous == nil || *d.Users.New.Previous != 10 || d.Events.Running != 2 ||
		d.Period.Previous == nil || len(d.Series.NewUsers) != 1 || d.Series.Activity[0].Attempts != 5 || d.Series.Mail == nil {
		t.Fatalf("body = %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"Value":12`) || !strings.Contains(w.Body.String(), `"NewUsers"`) {
		t.Fatalf("JSON must be PascalCase: %s", w.Body.String())
	}
	if w = ouServe(t, &ouUseCase{}, ouGrant(rbac.PermAnalyticsRead), "/api/analytics/overview?from=yesterday"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad period status = %d, want 400", w.Code)
	}
}

func TestUsersMapsTheReport(t *testing.T) {
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	uc := &ouUseCase{users: platformAnalyticsUseCase.UsersView{
		Total: 100, Blocked: 3, AvgDailyActive: 12.5,
		ByRole:        []platformAnalyticsUseCase.UsersRoleView{{Role: "user", Count: 99}},
		ActiveByDay:   []platformAnalyticsUseCase.UsersActiveDayView{{Day: day, DAU: 5, WAU: 9}},
		Methods:       []platformAnalyticsUseCase.UsersMethodView{{Method: "google", Total: 30, New: 4}},
		Retention:     platformAnalyticsUseCase.UsersRetentionView{One: 10, Two: 4, ThreePlus: 2, Never: 84},
		Registrations: []platformAnalyticsUseCase.OverviewDayNewView{{Day: day, New: 6}},
	}}
	w := ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead), "/api/analytics/users")
	var body struct {
		Data usersResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Data
	if d.Total != 100 || d.Blocked != 3 || d.AvgDailyActive != 12.5 || d.ByRole[0].Count != 99 || d.ActiveByDay[0].WAU != 9 ||
		d.Methods[0].Method != "google" || d.Retention.ThreePlus != 2 || d.Registrations[0].New != 6 {
		t.Fatalf("body = %s", w.Body.String())
	}
	// The aggregate report carries no per-user data.
	for _, key := range []string{"Email", "Name", "People"} {
		if strings.Contains(w.Body.String(), `"`+key+`"`) {
			t.Fatalf("aggregate users report leaks %q: %s", key, w.Body.String())
		}
	}
}

func TestOverviewCSV(t *testing.T) {
	prev := int64(8)
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	uc := &ouUseCase{overview: platformAnalyticsUseCase.OverviewView{
		Users: platformAnalyticsUseCase.OverviewUsersView{Total: 100, New: platformAnalyticsUseCase.OverviewMetricView{Value: 12, Previous: &prev}, Active: platformAnalyticsUseCase.OverviewMetricView{Value: 5}},
		Series: platformAnalyticsUseCase.OverviewSeriesView{
			NewUsers: []platformAnalyticsUseCase.OverviewDayNewView{{Day: day, New: 3}},
			Mail:     []platformAnalyticsUseCase.OverviewDayMailView{{Day: day.Add(24 * time.Hour), Sent: 7, Failed: 1}},
		},
	}}
	w := ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead), "/api/analytics/overview/export.csv?table=summary")
	rows := ouRows(t, w)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") || rows[0][0] != "Показник" {
		t.Fatalf("status = %d, header %q, rows %v", w.Code, w.Header().Get("Content-Type"), rows)
	}
	if rows[1][0] != "Акаунтів усього" || rows[1][1] != "100" || rows[1][2] != "" || rows[2][1] != "12" || rows[2][2] != "8" || rows[3][2] != "" {
		t.Fatalf("summary rows = %v", rows[:4])
	}
	rows = ouRows(t, ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead), "/api/analytics/overview/export.csv?table=series"))
	if len(rows) != 3 || rows[1][0] != "2026-09-29" || rows[1][1] != "3" || rows[2][0] != "2026-09-30" || rows[2][4] != "7" || rows[2][5] != "1" {
		t.Fatalf("series rows = %v", rows)
	}
	for _, target := range []string{"/api/analytics/overview/export.csv?table=nope", "/api/analytics/users/export.csv?table=nope", "/api/analytics/overview/export.csv"} {
		if w = ouServe(t, uc, ouGrant(rbac.PermAnalyticsRead), target); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 for an unknown table", target, w.Code)
		}
	}
}

func TestUsersCSV(t *testing.T) {
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	uc := &ouUseCase{
		users: platformAnalyticsUseCase.UsersView{
			Registrations: []platformAnalyticsUseCase.OverviewDayNewView{{Day: day, New: 6}},
			ActiveByDay:   []platformAnalyticsUseCase.UsersActiveDayView{{Day: day, DAU: 5, WAU: 9}},
			Methods:       []platformAnalyticsUseCase.UsersMethodView{{Method: "both", Total: 30, New: 4}},
			Retention:     platformAnalyticsUseCase.UsersRetentionView{One: 10, Two: 4, ThreePlus: 2, Never: 84},
		},
		people: []platformAnalyticsUseCase.UsersPersonView{{Name: "=HYPERLINK(\"x\")", Email: "+a@example.test", Role: "user", EventsJoined: 3, Solves: 7}},
	}
	prot := ouGrant(rbac.PermAnalyticsRead, rbac.PermAnalyticsUsersRead)
	get := func(table string) [][]string {
		return ouRows(t, ouServe(t, uc, prot, "/api/analytics/users/export.csv?table="+table))
	}
	if rows := get("registrations"); rows[0][0] != "Дата" || rows[1][0] != "2026-09-29" || rows[1][1] != "6" {
		t.Fatalf("registrations = %v", rows)
	}
	if rows := get("activity"); rows[1][1] != "5" || rows[1][2] != "9" {
		t.Fatalf("activity = %v", rows)
	}
	if rows := get("methods"); rows[1][0] != "Пароль і Google" || rows[1][1] != "30" || rows[1][2] != "4" {
		t.Fatalf("methods = %v", rows)
	}
	if rows := get("retention"); len(rows) != 5 || rows[3][1] != "2" || rows[4][1] != "84" {
		t.Fatalf("retention = %v", rows)
	}
	rows := get("people")
	if rows[0][0] != "Імʼя" || rows[1][0] != `'=HYPERLINK("x")` || rows[1][1] != "'+a@example.test" || rows[1][3] != "3" || rows[1][4] != "7" {
		t.Fatalf("people = %v (formulas must be neutralized)", rows)
	}
}
