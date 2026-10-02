package eventAnalytics

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
	"github.com/cybericebox/daemon/pkg/tools"
)

// TasksUseCase is the «Завдання» part of IUseCase.
type TasksUseCase interface {
	GetEventAnalyticsTasks(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.TasksView, error)
	GetEventAnalyticsTaskDetail(ctx context.Context, eventID, challengeID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.TaskDetailView, error)
	GetEventAnalyticsTaskWrongAnswers(ctx context.Context, eventID, challengeID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.WrongAnswersView, error)
}

// initTasks registers the «Завдання» routes on the analytics group. The wrong
// answer texts are behind requireSensitive; nothing else is.
func (h *Handler) initTasks(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("tasks", h.requireSections, h.tasks)
	analytics.GET("tasks/export.csv", h.requireSections, h.exportTasks)
	analytics.GET("tasks/:challengeID", h.requireSections, h.taskDetail)
	analytics.GET("tasks/:challengeID/wrong-answers", h.requireSensitive, h.taskWrongAnswers)
}

type (
	tasksResponse struct {
		Tasks  []taskRowResponse  `json:"Tasks"`
		Groups []groupRowResponse `json:"Groups"`
		Period periodResponse     `json:"Period"`
	}

	// taskRowResponse: the durations are seconds, null while nobody solved
	// the task; SolveRate is solvers ÷ teams that tried (0..1).
	taskRowResponse struct {
		ChallengeID      uuid.UUID           `json:"ChallengeID"`
		Name             string              `json:"Name"`
		Difficulty       string              `json:"Difficulty"`
		Points           int32               `json:"Points"`
		GroupID          uuid.UUID           `json:"GroupID"`
		GroupName        string              `json:"GroupName"`
		Attempts         int64               `json:"Attempts"`
		Correct          int64               `json:"Correct"`
		TeamsTried       int64               `json:"TeamsTried"`
		TeamsOpened      int64               `json:"TeamsOpened"`
		Solves           int64               `json:"Solves"`
		SolveRate        float64             `json:"SolveRate"`
		MedianSinceStart *int64              `json:"MedianSinceStartSeconds"`
		MedianSinceOpen  *int64              `json:"MedianSinceOpenSeconds"`
		FirstBloodTeam   string              `json:"FirstBloodTeam"`
		FirstBloodAt     *time.Time          `json:"FirstBloodAt"`
		HintsOpened      int64               `json:"HintsOpened"`
		HintPoints       int64               `json:"HintPoints"`
		Calibration      calibrationResponse `json:"Calibration"`
	}

	// calibrationResponse: Verdict is ok, too_easy, too_hard, insufficient
	// (fewer than 3 teams tried) or unknown (no known difficulty).
	calibrationResponse struct {
		Verdict     string  `json:"Verdict"`
		ExpectedMin float64 `json:"ExpectedMin"`
		ExpectedMax float64 `json:"ExpectedMax"`
	}

	groupRowResponse struct {
		GroupID    uuid.UUID `json:"GroupID"`
		GroupName  string    `json:"GroupName"`
		Tasks      int64     `json:"Tasks"`
		Attempts   int64     `json:"Attempts"`
		TeamsTried int64     `json:"TeamsTried"`
		Solves     int64     `json:"Solves"`
		SolveRate  float64   `json:"SolveRate"`
	}

	taskDetailResponse struct {
		Task        taskRowResponse       `json:"Task"`
		Series      []seriesPointResponse `json:"Series"`
		FailedTeams []failedTeamResponse  `json:"FailedTeams"`
		HintEffect  hintEffectResponse    `json:"HintEffect"`
		Period      periodResponse        `json:"Period"`
		RefreshedAt *time.Time            `json:"RefreshedAt"`
		Final       bool                  `json:"Final"`
	}

	failedTeamResponse struct {
		TeamID        uuid.UUID `json:"TeamID"`
		TeamName      string    `json:"TeamName"`
		Attempts      int64     `json:"Attempts"`
		LastAttemptAt time.Time `json:"LastAttemptAt"`
		HintsOpened   int64     `json:"HintsOpened"`
	}

	// hintEffectResponse compares the teams that unlocked a hint before
	// solving (or never solved) with the others. With's time runs from the
	// first unlock, Without's from the first open or attempt.
	hintEffectResponse struct {
		With    hintGroupResponse `json:"With"`
		Without hintGroupResponse `json:"Without"`
	}

	hintGroupResponse struct {
		Teams         int64   `json:"Teams"`
		Solved        int64   `json:"Solved"`
		SolveRate     float64 `json:"SolveRate"`
		MedianSeconds *int64  `json:"MedianSeconds"`
	}

	wrongAnswersResponse struct {
		Answers []wrongAnswerResponse `json:"Answers"`
		Period  periodResponse        `json:"Period"`
	}

	wrongAnswerResponse struct {
		Answer   string    `json:"Answer"`
		Attempts int64     `json:"Attempts"`
		Teams    int64     `json:"Teams"`
		LastAt   time.Time `json:"LastAt"`
	}
)

// tasks godoc
// @Summary Per-task analytics with the difficulty calibration and group summary
// @Description §6.3 «Завдання». Attempts, solves, solve rate (solvers ÷ teams that tried), median time to solve from the start and from the first open, first blood, hints and points spent, over the period (default: the event's window).
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=tasksResponse}
// @Router /events/{id}/manage/analytics/tasks [get]
func (h *Handler) tasks(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsTasks(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTasksResponse(v))
}

// exportTasks godoc
// @Summary Export the per-task analytics as CSV (UTF-8 with BOM)
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/tasks/export.csv [get]
func (h *Handler) exportTasks(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsTasks(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startAnalyticsCSV(ctx, "tasks")
	_ = w.Write([]string{"Завдання", "Група", "Складність", "Бали", "Спроби", "Правильні спроби", "Команд пробувало", "Команд відкривало", "Розв'язань",
		"Частка розв'язань", "Медіана від старту (с)", "Медіана від відкриття (с)", "Перша кров", "Час першої крові (UTC)", "Підказок", "Витрачено балів", "Калібрування"})
	for _, t := range v.Tasks {
		_ = w.Write([]string{csvCellText(t.Name), csvCellText(t.GroupName), t.Difficulty, strconv.Itoa(int(t.Points)),
			strconv.FormatInt(t.Attempts, 10), strconv.FormatInt(t.Correct, 10), strconv.FormatInt(t.TeamsTried, 10), strconv.FormatInt(t.TeamsOpened, 10),
			strconv.FormatInt(t.Solves, 10), csvRate(t.SolveRate), csvSeconds(t.MedianSinceStart), csvSeconds(t.MedianSinceOpen),
			csvCellText(t.FirstBloodTeam), csvTimePtr(t.FirstBloodAt), strconv.FormatInt(t.HintsOpened, 10), strconv.FormatInt(t.HintPoints, 10),
			t.Calibration.Verdict})
	}
	w.Flush()
}

// taskDetail godoc
// @Summary A task's analytics drawer
// @Description §6.3: solves over time (5-minute buckets), the teams that tried and failed, and the effect of hints. Wrong answer texts are a separate, restricted route.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=taskDetailResponse}
// @Router /events/{id}/manage/analytics/tasks/{challengeID} [get]
func (h *Handler) taskDetail(ctx *gin.Context) {
	eventID, challengeID, from, to, ok := parseTaskRequest(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsTaskDetail(ctx, eventID, challengeID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	series := make([]seriesPointResponse, 0, len(v.Series))
	for _, p := range v.Series {
		series = append(series, seriesPointResponse{At: p.At, Attempts: p.Attempts, Correct: p.Correct, Solves: p.Solves, Opens: p.Opens})
	}
	failed := make([]failedTeamResponse, 0, len(v.FailedTeams))
	for _, f := range v.FailedTeams {
		failed = append(failed, failedTeamResponse{TeamID: f.TeamID, TeamName: f.TeamName, Attempts: f.Attempts, LastAttemptAt: f.LastAttemptAt, HintsOpened: f.HintsOpened})
	}
	response.AbortWithData(ctx, taskDetailResponse{
		Task: toTaskRowResponse(v.Task), Series: series, FailedTeams: failed,
		HintEffect: hintEffectResponse{With: toHintGroupResponse(v.HintEffect.With), Without: toHintGroupResponse(v.HintEffect.Without)},
		Period:     periodResponse{From: v.Period.From, To: v.Period.To}, RefreshedAt: v.RefreshedAt, Final: v.Final,
	})
}

// taskWrongAnswers godoc
// @Summary The most common wrong answers of a task (restricted)
// @Description §7: answer texts are for the event owner, write moderators and platform admins only; read-only moderators get 403 (code 62202).
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=wrongAnswersResponse}
// @Router /events/{id}/manage/analytics/tasks/{challengeID}/wrong-answers [get]
func (h *Handler) taskWrongAnswers(ctx *gin.Context) {
	eventID, challengeID, from, to, ok := parseTaskRequest(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsTaskWrongAnswers(ctx, eventID, challengeID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	answers := make([]wrongAnswerResponse, 0, len(v.Answers))
	for _, a := range v.Answers {
		answers = append(answers, wrongAnswerResponse{Answer: a.Answer, Attempts: a.Attempts, Teams: a.Teams, LastAt: a.LastAt})
	}
	response.AbortWithData(ctx, wrongAnswersResponse{Answers: answers, Period: periodResponse{From: v.Period.From, To: v.Period.To}})
}

func parseTaskRequest(ctx *gin.Context) (eventID, challengeID uuid.UUID, from, to *time.Time, ok bool) {
	if eventID, ok = parseEventID(ctx); !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return eventID, uuid.Nil, nil, nil, false
	}
	from, to, ok = parsePeriod(ctx)
	return eventID, challengeID, from, to, ok
}

func toTasksResponse(v eventAnalyticsUseCase.TasksView) tasksResponse {
	tasks := make([]taskRowResponse, 0, len(v.Tasks))
	for _, t := range v.Tasks {
		tasks = append(tasks, toTaskRowResponse(t))
	}
	groups := make([]groupRowResponse, 0, len(v.Groups))
	for _, g := range v.Groups {
		groups = append(groups, groupRowResponse{GroupID: g.GroupID, GroupName: g.GroupName, Tasks: g.Tasks, Attempts: g.Attempts, TeamsTried: g.TeamsTried, Solves: g.Solves, SolveRate: g.SolveRate})
	}
	return tasksResponse{Tasks: tasks, Groups: groups, Period: periodResponse{From: v.Period.From, To: v.Period.To}}
}

func toTaskRowResponse(t eventAnalyticsUseCase.TaskRowView) taskRowResponse {
	return taskRowResponse{
		ChallengeID: t.ChallengeID, Name: t.Name, Difficulty: t.Difficulty, Points: t.Points, GroupID: t.GroupID, GroupName: t.GroupName,
		Attempts: t.Attempts, Correct: t.Correct, TeamsTried: t.TeamsTried, TeamsOpened: t.TeamsOpened, Solves: t.Solves, SolveRate: t.SolveRate,
		MedianSinceStart: t.MedianSinceStart, MedianSinceOpen: t.MedianSinceOpen,
		FirstBloodTeam: t.FirstBloodTeam, FirstBloodAt: t.FirstBloodAt, HintsOpened: t.HintsOpened, HintPoints: t.HintPoints,
		Calibration: calibrationResponse{Verdict: t.Calibration.Verdict, ExpectedMin: t.Calibration.ExpectedMin, ExpectedMax: t.Calibration.ExpectedMax},
	}
}

func toHintGroupResponse(g eventAnalyticsUseCase.HintGroupView) hintGroupResponse {
	return hintGroupResponse{Teams: g.Teams, Solved: g.Solved, SolveRate: g.SolveRate, MedianSeconds: g.MedianSeconds}
}

// startAnalyticsCSV sends the CSV download headers and the UTF-8 BOM (Excel
// reads Cyrillic correctly only with it).
func startAnalyticsCSV(ctx *gin.Context, name string) *csv.Writer {
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Type", "text/csv; charset=utf-8")
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="analytics-%s-%s.csv"`, name, time.Now().UTC().Format("20060102-1504")))
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Status(http.StatusOK)
	_, _ = ctx.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	return csv.NewWriter(ctx.Writer)
}

// csvCellText neutralizes spreadsheet formulas in user-controlled text.
func csvCellText(value string) string { return tools.CSVText(value) }

func csvTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func csvSeconds(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func csvRate(value float64) string { return strconv.FormatFloat(value, 'f', 3, 64) }
