package eventAnalytics

import (
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

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// fakeUseCase serves the overview and access routes; the section calls
// panic on the nil embedded interface.
type fakeUseCase struct {
	IUseCase
	levels    []eventAnalyticsUseCase.Level
	deny      error
	from, to  *time.Time
	overview  eventAnalyticsUseCase.OverviewView
	sensitive bool
	people    bool
	usage     eventAnalyticsUseCase.UsageView
	usageTeam *uuid.UUID
	usageFrom *time.Time
	usageTo   *time.Time
}

func (f *fakeUseCase) RequireEventAnalytics(_ context.Context, _ uuid.UUID, _ rbac.Claims, level eventAnalyticsUseCase.Level) error {
	f.levels = append(f.levels, level)
	return f.deny
}

func (f *fakeUseCase) EventAnalyticsAccess(context.Context, uuid.UUID, rbac.Claims) (eventAnalyticsModel.Access, error) {
	return eventAnalyticsModel.Access{Sections: true, Sensitive: f.sensitive, People: f.people}, nil
}

func (f *fakeUseCase) GetEventAnalyticsOverview(_ context.Context, _ uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.OverviewView, error) {
	f.from, f.to = from, to
	return f.overview, nil
}

func (f *fakeUseCase) GetEventAnalyticsUsage(_ context.Context, _ uuid.UUID, team *uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.UsageView, error) {
	f.usageTeam, f.usageFrom, f.usageTo = team, from, to
	return f.usage, nil
}

type allowAll struct{}

func (allowAll) RequirePermission(rbac.Permission) gin.HandlerFunc { return func(*gin.Context) {} }

func serve(t *testing.T, uc *fakeUseCase, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleUser}))
	}, response.WithErrorHandler)
	NewEventAnalyticsAPIHandler(uc, allowAll{}).Init(router.Group("api"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestOverviewNeedsSectionAccessAndPassesThePeriod(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	uc := &fakeUseCase{overview: eventAnalyticsUseCase.OverviewView{
		Attempts: 7, Teams: eventAnalyticsUseCase.TeamCountsView{Total: 3, Admitted: 2, Incomplete: 1},
		Series:  []eventAnalyticsUseCase.SeriesPointView{{At: start, Attempts: 7}},
		Markers: eventAnalyticsUseCase.MarkersView{StartAt: start},
	}}
	eventID := uuid.Must(uuid.NewV7())
	w := serve(t, uc, "/api/events/"+eventID.String()+"/manage/analytics/overview?from=2026-09-29T10:00:00Z&to=2026-09-29T12:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if len(uc.levels) != 1 || uc.levels[0] != eventAnalyticsUseCase.LevelSections {
		t.Fatalf("access levels checked: %v", uc.levels)
	}
	if uc.from == nil || !uc.from.Equal(start) || uc.to == nil || !uc.to.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("period: %v %v", uc.from, uc.to)
	}
	var body struct {
		Data overviewResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Attempts != 7 || body.Data.Teams.Incomplete != 1 || len(body.Data.Series) != 1 || !body.Data.Markers.StartAt.Equal(start) {
		t.Fatalf("body: %+v", body.Data)
	}
}

func TestOverviewDeniedWithoutAccess(t *testing.T) {
	uc := &fakeUseCase{deny: eventAnalyticsModel.ErrEventAnalyticsForbidden.Err()}
	w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/overview")
	if w.Code != http.StatusForbidden || uc.from != nil {
		t.Fatalf("status = %d, want 403 and no report", w.Code)
	}
}

func TestOverviewRejectsABadPeriod(t *testing.T) {
	uc := &fakeUseCase{}
	w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/overview?from=yesterday")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// requireSensitive is the gate section agents put on wrong answers and
// integrity routes.
func TestRequireSensitiveChecksTheSensitiveLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &fakeUseCase{deny: eventAnalyticsModel.ErrEventAnalyticsSensitiveForbidden.Err()}
	h := NewEventAnalyticsAPIHandler(uc, allowAll{})
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	eventID := uuid.Must(uuid.NewV7())
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7())}))
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	h.requireSensitive(ctx)
	if !ctx.IsAborted() || len(uc.levels) != 1 || uc.levels[0] != eventAnalyticsUseCase.LevelSensitive {
		t.Fatalf("aborted=%v levels=%v", ctx.IsAborted(), uc.levels)
	}
}

// Every download (section CSVs, the report ZIP) checks the export level, so a
// read-only moderator (observer) gets 403 and no file.
func TestEveryExportRouteNeedsTheExportLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewEventAnalyticsAPIHandler(&fakeUseCase{}, allowAll{}).Init(router.Group("api"))
	eventID := uuid.Must(uuid.NewV7()).String()
	exports := 0
	for _, route := range router.Routes() {
		if !strings.Contains(route.Path, "/export.") {
			continue
		}
		exports++
		uc := &fakeUseCase{deny: eventAnalyticsModel.ErrEventAnalyticsExportForbidden.Err()}
		w := serve(t, uc, strings.ReplaceAll(route.Path, ":id", eventID))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", route.Path, w.Code)
		}
		if len(uc.levels) != 1 || (uc.levels[0] != eventAnalyticsUseCase.LevelExport && !(strings.Contains(route.Path, "/integrity/") && uc.levels[0] == eventAnalyticsUseCase.LevelSensitive)) {
			t.Errorf("%s: levels checked %v, want the export level", route.Path, uc.levels)
		}
	}
	if exports < 10 {
		t.Fatalf("found %d export routes, want every section's CSV and the report ZIP", exports)
	}
}

func TestAccessReportsTheViewersLevels(t *testing.T) {
	uc := &fakeUseCase{}
	w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/access")
	var body struct {
		Data accessResponse `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || !body.Data.Sections || body.Data.Sensitive {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

// The mail counters of the overview are for the sensitive access only.
func TestOverviewMailCountersNeedTheSensitiveAccess(t *testing.T) {
	comms := eventAnalyticsUseCase.OverviewSnapshots{Comms: eventAnalyticsUseCase.CommsSnapshotView{EmailSent: 5, EmailFailed: 1}}
	for _, sensitive := range []bool{false, true} {
		uc := &fakeUseCase{sensitive: sensitive, overview: eventAnalyticsUseCase.OverviewView{OverviewSnapshots: comms}}
		w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/overview")
		var body struct {
			Data overviewResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if got := body.Data.Comms != nil; got != sensitive {
			t.Fatalf("sensitive=%v: comms present = %v", sensitive, got)
		}
		if sensitive && body.Data.Comms.EmailSent != 5 {
			t.Fatalf("comms: %+v", body.Data.Comms)
		}
	}
}

// «Використання» for a viewer without the people access (a read-only moderator): the aggregates only, no
// per-person rows, names or times, and no per-person CSV. The roles behind People are in TestResolveAccess.
func TestUsagePeopleOnlyWithThePeopleAccess(t *testing.T) {
	seen := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	usage := eventAnalyticsUseCase.UsageView{Available: true, Summary: eventAnalyticsUseCase.UsageSummaryView{Users: 12, OnlineNow: 4},
		Users: []eventAnalyticsUseCase.UsageUserView{{UserID: uuid.Must(uuid.NewV7()), UserName: "Олена Коваленко", LastSeenAt: &seen}}}
	for _, people := range []bool{false, true} {
		uc := &fakeUseCase{people: people, usage: usage}
		base := "/api/events/" + uuid.Must(uuid.NewV7()).String() + "/manage/analytics/usage"
		w := serve(t, uc, base)
		var body struct {
			Data usageResponse `json:"data"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("people=%v: status = %d body = %s", people, w.Code, w.Body.String())
		}
		if body.Data.Summary.Users != 12 || body.Data.Summary.OnlineNow != 4 {
			t.Fatalf("people=%v: summary %+v", people, body.Data.Summary)
		}
		if people != (len(body.Data.Users) == 1) || body.Data.PeopleHidden == people || bytes.Contains(w.Body.Bytes(), []byte("Олена")) != people {
			t.Fatalf("people=%v: body %s", people, w.Body.String())
		}
		w = serve(t, uc, base+"/export.csv")
		if (w.Code == http.StatusOK) != people {
			t.Fatalf("people=%v: CSV status %d", people, w.Code)
		}
	}
}

func TestAccessReportsThePeopleLevel(t *testing.T) {
	uc := &fakeUseCase{people: true}
	w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/access")
	var body struct {
		Data accessResponse `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || !body.Data.People {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

// A viewer with the totals only always gets them for the whole event: a team or a narrow period would single out
// one person. The people access keeps both filters.
func TestUsageTotalsIgnoreTheFiltersWithoutThePeopleAccess(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	for _, people := range []bool{false, true} {
		uc := &fakeUseCase{people: people, usage: eventAnalyticsUseCase.UsageView{Available: true}}
		w := serve(t, uc, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/usage?team="+team.String()+
			"&from=2026-10-19T10:00:00Z&to=2026-10-19T10:05:00Z")
		if w.Code != http.StatusOK {
			t.Fatalf("people=%v: status = %d body = %s", people, w.Code, w.Body.String())
		}
		filtered := uc.usageTeam != nil && uc.usageFrom != nil && uc.usageTo != nil
		if filtered != people || (!people && (uc.usageTeam != nil || uc.usageFrom != nil || uc.usageTo != nil)) {
			t.Fatalf("people=%v: team=%v from=%v to=%v", people, uc.usageTeam, uc.usageFrom, uc.usageTo)
		}
	}
}
