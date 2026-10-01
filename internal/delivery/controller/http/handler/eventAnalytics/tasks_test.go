package eventAnalytics

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

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// taskUseCase serves the «Завдання» and «Прогрес» routes; any other use-case
// call panics on the nil embedded interface.
type taskUseCase struct {
	IUseCase
	levels    []eventAnalyticsUseCase.Level
	deny      map[eventAnalyticsUseCase.Level]error
	tasks     eventAnalyticsUseCase.TasksView
	matrix    eventAnalyticsUseCase.MatrixView
	inactive  eventAnalyticsUseCase.InactiveView
	scoreTop  int
	scoreTeam []uuid.UUID
	minutes   int
	wrongHit  bool
}

func (f *taskUseCase) RequireEventAnalytics(_ context.Context, _ uuid.UUID, _ rbac.Claims, level eventAnalyticsUseCase.Level) error {
	f.levels = append(f.levels, level)
	return f.deny[level]
}
func (f *taskUseCase) GetEventAnalyticsTasks(context.Context, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.TasksView, error) {
	return f.tasks, nil
}
func (f *taskUseCase) GetEventAnalyticsTaskWrongAnswers(context.Context, uuid.UUID, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.WrongAnswersView, error) {
	f.wrongHit = true
	return eventAnalyticsUseCase.WrongAnswersView{Answers: []eventAnalyticsUseCase.WrongAnswerView{{Answer: "ICE{x}", Attempts: 4, Teams: 2}}}, nil
}
func (f *taskUseCase) GetEventAnalyticsProgressScores(_ context.Context, _ uuid.UUID, _, _ *time.Time, top int, teams []uuid.UUID) (eventAnalyticsUseCase.ScoresView, error) {
	f.scoreTop, f.scoreTeam = top, teams
	return eventAnalyticsUseCase.ScoresView{}, nil
}
func (f *taskUseCase) GetEventAnalyticsProgressMatrix(context.Context, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.MatrixView, error) {
	return f.matrix, nil
}
func (f *taskUseCase) GetEventAnalyticsProgressInactive(_ context.Context, _ uuid.UUID, minutes int) (eventAnalyticsUseCase.InactiveView, error) {
	f.minutes = minutes
	return f.inactive, nil
}

func serveTasks(t *testing.T, uc *taskUseCase, target string) *httptest.ResponseRecorder {
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

func analyticsURL(path string) string {
	return "/api/events/" + uuid.Must(uuid.NewV7()).String() + "/manage/analytics/" + path
}

func TestTasksReport(t *testing.T) {
	median := int64(90)
	uc := &taskUseCase{tasks: eventAnalyticsUseCase.TasksView{Tasks: []eventAnalyticsUseCase.TaskRowView{{
		Name: "Web 1", Solves: 2, MedianSinceStart: &median,
		Calibration: eventAnalyticsUseCase.CalibrationView{Verdict: eventAnalyticsModel.CalibrationTooHard},
	}}}}
	w := serveTasks(t, uc, analyticsURL("tasks"))
	if w.Code != http.StatusOK || len(uc.levels) != 1 || uc.levels[0] != eventAnalyticsUseCase.LevelSections {
		t.Fatalf("status = %d levels = %v", w.Code, uc.levels)
	}
	var body struct {
		Data tasksResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	row := body.Data.Tasks[0]
	if row.Name != "Web 1" || row.MedianSinceStart == nil || *row.MedianSinceStart != 90 || row.Calibration.Verdict != "too_hard" || row.MedianSinceOpen != nil {
		t.Fatalf("row: %+v", row)
	}
}

func TestTasksExportIsCSVWithBOMAndNeutralizesFormulas(t *testing.T) {
	uc := &taskUseCase{tasks: eventAnalyticsUseCase.TasksView{Tasks: []eventAnalyticsUseCase.TaskRowView{{Name: "=1+1", Attempts: 3}}}}
	w := serveTasks(t, uc, analyticsURL("tasks/export.csv"))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("status = %d type = %q", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") || !strings.Contains(body, "'=1+1") || strings.Count(body, "\n") != 2 {
		t.Fatalf("csv: %q", body)
	}
}

// Wrong answer texts are behind the sensitive level; the use case is not
// reached without it.
func TestWrongAnswersNeedTheSensitiveLevel(t *testing.T) {
	uc := &taskUseCase{deny: map[eventAnalyticsUseCase.Level]error{eventAnalyticsUseCase.LevelSensitive: eventAnalyticsModel.ErrEventAnalyticsSensitiveForbidden.Err()}}
	target := analyticsURL("tasks/" + uuid.Must(uuid.NewV7()).String() + "/wrong-answers")
	w := serveTasks(t, uc, target)
	if w.Code != http.StatusForbidden || uc.wrongHit {
		t.Fatalf("status = %d, use case reached = %v", w.Code, uc.wrongHit)
	}
	if len(uc.levels) != 1 || uc.levels[0] != eventAnalyticsUseCase.LevelSensitive {
		t.Fatalf("levels: %v", uc.levels)
	}

	uc = &taskUseCase{}
	w = serveTasks(t, uc, target)
	if w.Code != http.StatusOK || !uc.wrongHit || !strings.Contains(w.Body.String(), "ICE{x}") {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func TestTaskDetailRejectsABadChallengeID(t *testing.T) {
	w := serveTasks(t, &taskUseCase{}, analyticsURL("tasks/not-a-uuid"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestProgressScoresParsesTopAndTeams(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &taskUseCase{}
	w := serveTasks(t, uc, analyticsURL("progress/scores?top=5&teams="+a.String()+","+b.String()))
	if w.Code != http.StatusOK || uc.scoreTop != 5 || len(uc.scoreTeam) != 2 || uc.scoreTeam[0] != a || uc.scoreTeam[1] != b {
		t.Fatalf("status = %d top = %d teams = %v", w.Code, uc.scoreTop, uc.scoreTeam)
	}
	uc = &taskUseCase{}
	serveTasks(t, uc, analyticsURL("progress/scores"))
	if uc.scoreTop != eventAnalyticsUseCase.DefaultScoreTop {
		t.Fatalf("default top = %d", uc.scoreTop)
	}
	for _, bad := range []string{"?top=-1", "?top=x", "?teams=nope"} {
		if w := serveTasks(t, &taskUseCase{}, analyticsURL("progress/scores"+bad)); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", bad, w.Code)
		}
	}
}

func TestProgressMatrixExportLaysOutSolvedTriedUntouched(t *testing.T) {
	team, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	solved := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	uc := &taskUseCase{matrix: eventAnalyticsUseCase.MatrixView{
		Tasks: []eventAnalyticsUseCase.MatrixTaskView{{ChallengeID: a, Name: "A"}, {ChallengeID: b, Name: "B"}, {ChallengeID: c, Name: "C"}},
		Teams: []eventAnalyticsUseCase.MatrixTeamView{{TeamID: team, Name: "Blue", Points: 100, Solved: 1}, {TeamID: other, Name: "Red"}},
		Cells: []eventAnalyticsUseCase.MatrixCellView{
			{TeamID: team, ChallengeID: a, Attempts: 2, SolvedAt: &solved},
			{TeamID: team, ChallengeID: b, Attempts: 4},
		},
	}}
	w := serveTasks(t, uc, analyticsURL("progress/matrix/export.csv"))
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(w.Body.String(), "\xEF\xBB\xBF")), "\n")
	if w.Code != http.StatusOK || len(lines) != 3 {
		t.Fatalf("status = %d csv = %q", w.Code, w.Body.String())
	}
	if !strings.HasSuffix(lines[1], "2026-09-29T12:00:00Z,спроб: 4,") || !strings.HasSuffix(lines[2], ",,,") {
		t.Fatalf("rows: %q", lines)
	}
}

func TestProgressInactivePassesTheThreshold(t *testing.T) {
	uc := &taskUseCase{}
	if w := serveTasks(t, uc, analyticsURL("progress/inactive?minutes=45")); w.Code != http.StatusOK || uc.minutes != 45 {
		t.Fatalf("status = %d minutes = %d", w.Code, uc.minutes)
	}
	uc = &taskUseCase{}
	serveTasks(t, uc, analyticsURL("progress/inactive"))
	if uc.minutes != eventAnalyticsUseCase.DefaultInactiveMinutes {
		t.Fatalf("default minutes = %d", uc.minutes)
	}
	if w := serveTasks(t, &taskUseCase{}, analyticsURL("progress/inactive?minutes=x")); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// fakeUseCase (handler_test.go) satisfies the report parts with empty answers.
func (f *fakeUseCase) GetEventAnalyticsTasks(context.Context, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.TasksView, error) {
	return eventAnalyticsUseCase.TasksView{}, nil
}
func (f *fakeUseCase) GetEventAnalyticsTaskDetail(context.Context, uuid.UUID, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.TaskDetailView, error) {
	return eventAnalyticsUseCase.TaskDetailView{}, nil
}
func (f *fakeUseCase) GetEventAnalyticsTaskWrongAnswers(context.Context, uuid.UUID, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.WrongAnswersView, error) {
	return eventAnalyticsUseCase.WrongAnswersView{}, nil
}
func (f *fakeUseCase) GetEventAnalyticsProgressScores(context.Context, uuid.UUID, *time.Time, *time.Time, int, []uuid.UUID) (eventAnalyticsUseCase.ScoresView, error) {
	return eventAnalyticsUseCase.ScoresView{}, nil
}
func (f *fakeUseCase) GetEventAnalyticsProgressMatrix(context.Context, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.MatrixView, error) {
	return eventAnalyticsUseCase.MatrixView{}, nil
}
func (f *fakeUseCase) GetEventAnalyticsProgressHeatmap(context.Context, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.HeatmapView, error) {
	return eventAnalyticsUseCase.HeatmapView{}, nil
}
func (f *fakeUseCase) GetEventAnalyticsProgressInactive(context.Context, uuid.UUID, int) (eventAnalyticsUseCase.InactiveView, error) {
	return eventAnalyticsUseCase.InactiveView{}, nil
}
