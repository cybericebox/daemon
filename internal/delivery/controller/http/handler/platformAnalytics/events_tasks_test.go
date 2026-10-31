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

// catalogUseCase serves the events and tasks routes; any other call panics on
// the nil embedded interface.
type catalogUseCase struct {
	IUseCase
	events          platformAnalyticsUseCase.EventsView
	tasks           platformAnalyticsUseCase.TasksView
	calls           int
	category, level string
	tableQuery      platformAnalyticsUseCase.TableQuery
}

func (f *catalogUseCase) GetEvents(context.Context, *time.Time, *time.Time) (platformAnalyticsUseCase.EventsView, error) {
	f.calls++
	return f.events, nil
}

func (f *catalogUseCase) GetEventsTable(_ context.Context, _, _ *time.Time, q platformAnalyticsUseCase.TableQuery) (platformAnalyticsUseCase.EventsTable, error) {
	f.calls++
	f.tableQuery = q
	return platformAnalyticsUseCase.EventsTable{Rows: f.events.Events, Total: int64(len(f.events.Events))}, nil
}

func (f *catalogUseCase) GetTasksTable(_ context.Context, _, _ *time.Time, category, level string, q platformAnalyticsUseCase.TableQuery) (platformAnalyticsUseCase.TasksTable, error) {
	f.calls++
	f.category, f.level, f.tableQuery = category, level, q
	return platformAnalyticsUseCase.TasksTable{Rows: f.tasks.Tasks, Total: int64(len(f.tasks.Tasks))}, nil
}

func (f *catalogUseCase) GetTasks(_ context.Context, _, _ *time.Time, category, level string) (platformAnalyticsUseCase.TasksView, error) {
	f.calls++
	f.category, f.level = category, level
	return f.tasks, nil
}

// catalogProt records the permission the group asks for and can deny it.
type catalogProt struct {
	deny      bool
	permitted []rbac.Permission
}

func (p *catalogProt) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	p.permitted = append(p.permitted, required)
	return func(ctx *gin.Context) {
		if p.deny {
			ctx.AbortWithStatus(http.StatusForbidden)
		}
	}
}

func serveCatalog(t *testing.T, uc *catalogUseCase, prot *catalogProt, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewPlatformAnalyticsAPIHandler(uc, prot).Init(router.Group("api"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestCatalogRoutesAreStaffGated(t *testing.T) {
	for _, target := range []string{
		"/api/analytics/events", "/api/analytics/events/export.csv?table=events",
		"/api/analytics/tasks", "/api/analytics/tasks/export.csv?table=tasks",
	} {
		uc := &catalogUseCase{}
		prot := &catalogProt{deny: true}
		w := serveCatalog(t, uc, prot, target)
		if w.Code != http.StatusForbidden || uc.calls != 0 {
			t.Fatalf("%s: status = %d, use case calls = %d, want 403 and none", target, w.Code, uc.calls)
		}
		found := false
		for _, perm := range prot.permitted {
			found = found || perm == rbac.PermAnalyticsRead
		}
		if !found {
			t.Fatalf("%s: analytics.read was not required", target)
		}
	}
}

func TestEventsResponseAndCSV(t *testing.T) {
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	duration := int64(3600)
	finish := day.Add(time.Hour)
	uc := &catalogUseCase{events: platformAnalyticsUseCase.EventsView{
		Totals: platformAnalyticsUseCase.EventsTotals{Events: 2, Registrations: 7},
		Series: []platformAnalyticsUseCase.EventDayView{{Day: day, EventsCreated: 1, EventsStarted: 1, Registrations: 7}},
		Events: []platformAnalyticsUseCase.EventRowView{
			{ID: uuid.Must(uuid.NewV7()), Tag: "ctf", Name: "=HYPERLINK(\"x\")", Status: "finished", StartAt: day, FinishAt: &finish, DurationSeconds: &duration, Participants: 4, Teams: 2, Solves: 3, TeamsSolved: 1, CompletionRate: 0.5},
			{ID: uuid.Must(uuid.NewV7()), Tag: "draft", Name: "Draft", Status: "not_published"},
		},
		EventsTotal: 2, EventsLimit: 200,
		Upcoming: []platformAnalyticsUseCase.UpcomingEventView{{Tag: "next", Name: "Next", StartAt: day.Add(48 * time.Hour), Published: true, Registrations: 3}},
	}}

	w := serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events?from=2026-09-01T00:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	var body struct {
		Data eventsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := body.Data
	if got.Totals.Registrations != 7 || len(got.Events) != 2 || got.Events[0].CompletionRate != 0.5 || got.Events[0].DurationSeconds == nil ||
		got.Events[1].StartAt != nil || got.EventsTotal != 2 || got.EventsLimit != 200 || len(got.Upcoming) != 1 {
		t.Fatalf("response = %+v", got)
	}
	if !strings.Contains(w.Body.String(), `"CompletionRate"`) || !strings.Contains(w.Body.String(), `"DurationSeconds"`) {
		t.Fatalf("keys are not PascalCase: %s", w.Body)
	}

	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events/export.csv?table=events")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("csv status = %d, type = %q", w.Code, w.Header().Get("Content-Type"))
	}
	raw := w.Body.Bytes()
	if len(raw) < 3 || raw[0] != 0xEF || raw[1] != 0xBB || raw[2] != 0xBF {
		t.Fatal("csv has no BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw[3:]))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][0] != "Захід" || rows[1][0] != `'=HYPERLINK("x")` || rows[1][10] != "3600" || rows[2][3] != "" || rows[2][10] != "" {
		t.Fatalf("events csv = %q", rows)
	}
	for table, header := range map[string]string{"series": "День (UTC)", "upcoming": "Захід"} {
		w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events/export.csv?table="+table)
		rows, err = csv.NewReader(strings.NewReader(string(w.Body.Bytes()[3:]))).ReadAll()
		if err != nil || len(rows) != 2 || rows[0][0] != header {
			t.Fatalf("%s csv = %q, %v", table, rows, err)
		}
	}
	uc.calls = 0
	if w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events/export.csv?table=nope"); w.Code != http.StatusBadRequest || uc.calls != 0 {
		t.Fatalf("unknown table: status = %d, calls = %d", w.Code, uc.calls)
	}
	if w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events?from=yesterday"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad period: status = %d", w.Code)
	}
}

func TestTasksResponseAndCSV(t *testing.T) {
	median := int64(90)
	row := platformAnalyticsUseCase.TaskRowView{
		Exercise: "@Web", Task: "Login", Level: "easy", Categories: []string{"web", "sql"}, EventsUsed: 2, Attempts: 10, TeamsTried: 4,
		TeamsEngaged: 5, Solves: 3, TeamsHinted: 1, SolveRate: 0.75, HintRate: 0.2, MedianSolveSeconds: &median, Calibration: "ok",
	}
	dead := platformAnalyticsUseCase.TaskRowView{Exercise: "Misc", Task: "Dead", Categories: []string{}, EventsUsed: 1, Calibration: "unknown"}
	uc := &catalogUseCase{tasks: platformAnalyticsUseCase.TasksView{
		Totals:     platformAnalyticsUseCase.TasksTotals{TasksUsed: 2, NeverSolved: 1},
		Categories: []string{"sql", "web"}, Levels: []string{"trivial", "easy"},
		Tasks: []platformAnalyticsUseCase.TaskRowView{row, dead}, TasksTotal: 2, TasksLimit: 200,
		Unsolved: []platformAnalyticsUseCase.TaskRowView{dead}, UnsolvedTotal: 1,
	}}

	w := serveCatalog(t, uc, &catalogProt{}, "/api/analytics/tasks?category=web&level=easy")
	if w.Code != http.StatusOK || uc.category != "web" || uc.level != "easy" {
		t.Fatalf("status = %d, filters = %q/%q: %s", w.Code, uc.category, uc.level, w.Body)
	}
	var body struct {
		Data tasksResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := body.Data
	if len(got.Tasks) != 2 || got.Tasks[0].MedianSolveSeconds == nil || got.Tasks[1].MedianSolveSeconds != nil || len(got.Unsolved) != 1 ||
		got.UnsolvedTotal != 1 || len(got.Categories) != 2 || len(got.Levels) != 2 || got.ByCategory == nil || got.ByLevel == nil {
		t.Fatalf("response = %+v", got)
	}

	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/tasks/export.csv?table=tasks")
	rows, err := csv.NewReader(strings.NewReader(string(w.Body.Bytes()[3:]))).ReadAll()
	if err != nil || len(rows) != 3 || rows[1][0] != "'@Web" || rows[1][3] != "web, sql" || rows[1][10] != "90" || rows[2][10] != "" {
		t.Fatalf("tasks csv = %q, %v", rows, err)
	}
	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/tasks/export.csv?table=unsolved")
	rows, err = csv.NewReader(strings.NewReader(string(w.Body.Bytes()[3:]))).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][1] != "Dead" {
		t.Fatalf("unsolved csv = %q, %v", rows, err)
	}
	uc.calls = 0
	if w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/tasks/export.csv?table=events"); w.Code != http.StatusBadRequest || uc.calls != 0 {
		t.Fatalf("unknown table: status = %d, calls = %d", w.Code, uc.calls)
	}
}

// Every report can come with the previous period; the tables are paged and sorted on the server and the CSV of the
// same filters is not cut.
func TestCompareWithThePreviousPeriodAndTheEventTables(t *testing.T) {
	uc := &catalogUseCase{events: platformAnalyticsUseCase.EventsView{Events: []platformAnalyticsUseCase.EventRowView{{Tag: "ctf", Name: "CTF", Status: "finished"}}}}

	w := serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events?from=2026-09-08T00:00:00Z&to=2026-09-15T00:00:00Z&compare=previous")
	var body struct {
		Data struct {
			Current        json.RawMessage
			Previous       json.RawMessage
			PreviousPeriod struct{ From, To time.Time }
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status = %d err = %v body = %s", w.Code, err, w.Body)
	}
	if len(body.Data.Current) == 0 || len(body.Data.Previous) == 0 || body.Data.PreviousPeriod.From.Day() != 1 || body.Data.PreviousPeriod.To.Day() != 8 {
		t.Fatalf("envelope = %s", w.Body)
	}
	// all time has no previous period
	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events?compare=previous")
	if !strings.Contains(w.Body.String(), `"Previous":null`) {
		t.Fatalf("all time: %s", w.Body)
	}
	// without compare the answer is the report itself
	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events?from=2026-09-08T00:00:00Z")
	if strings.Contains(w.Body.String(), "PreviousPeriod") {
		t.Fatalf("no compare, got an envelope: %s", w.Body)
	}

	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events/table?page=3&pageSize=10&search=ctf&sortBy=participants&sortDir=asc")
	if w.Code != http.StatusOK || uc.tableQuery.Page != 3 || uc.tableQuery.Search != "ctf" || uc.tableQuery.SortBy != "participants" {
		t.Fatalf("table: status = %d query = %+v", w.Code, uc.tableQuery)
	}
	if w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events/table?page=1&sortBy=nope"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown column: status = %d", w.Code)
	}
	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/events/export.csv?table=events&search=ctf&sortBy=name")
	if w.Code != http.StatusOK || uc.tableQuery.Page != 0 || uc.tableQuery.Search != "ctf" {
		t.Fatalf("csv: status = %d query = %+v", w.Code, uc.tableQuery)
	}
	w = serveCatalog(t, uc, &catalogProt{}, "/api/analytics/tasks/table?page=1&category=web&level=easy&search=sql&sortBy=solveRate")
	if w.Code != http.StatusOK || uc.category != "web" || uc.level != "easy" || uc.tableQuery.SortBy != "solveRate" {
		t.Fatalf("tasks table: status = %d", w.Code)
	}
}
