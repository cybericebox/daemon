package eventAnalytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}

func (f *fakeUseCase) RequireEventAnalytics(_ context.Context, _ uuid.UUID, _ rbac.Claims, level eventAnalyticsUseCase.Level) error {
	f.levels = append(f.levels, level)
	return f.deny
}

func (f *fakeUseCase) EventAnalyticsAccess(context.Context, uuid.UUID, rbac.Claims) (eventAnalyticsModel.Access, error) {
	return eventAnalyticsModel.Access{Sections: true, Sensitive: f.sensitive}, nil
}

func (f *fakeUseCase) GetEventAnalyticsOverview(_ context.Context, _ uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.OverviewView, error) {
	f.from, f.to = from, to
	return f.overview, nil
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
