package platformAnalytics

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// TasksUseCase is the port of the tasks section.
type TasksUseCase interface {
	GetTasks(ctx context.Context, from, to *time.Time, category, level string) (platformAnalyticsUseCase.TasksView, error)
}

func (h *Handler) initTasks(router *gin.RouterGroup) {
	analytics := router.Group("analytics", h.prot.RequirePermission(rbac.PermAnalyticsRead))
	analytics.GET("tasks", h.tasks)
	analytics.GET("tasks/export.csv", h.exportTasks)
}

type (
	tasksResponse struct {
		Period        periodResponse          `json:"Period"`
		Totals        tasksTotalsResponse     `json:"Totals"`
		Categories    []string                `json:"Categories"`
		Levels        []string                `json:"Levels"`
		Tasks         []taskRowResponse       `json:"Tasks"`
		TasksTotal    int64                   `json:"TasksTotal"`
		TasksLimit    int                     `json:"TasksLimit"`
		Unsolved      []taskRowResponse       `json:"Unsolved"`
		UnsolvedTotal int64                   `json:"UnsolvedTotal"`
		ByCategory    []categoryUsageResponse `json:"ByCategory"`
		ByLevel       []levelSolveResponse    `json:"ByLevel"`
	}

	tasksTotalsResponse struct {
		TasksUsed   int64   `json:"TasksUsed"`
		Uses        int64   `json:"Uses"`
		Attempts    int64   `json:"Attempts"`
		Solves      int64   `json:"Solves"`
		NeverSolved int64   `json:"NeverSolved"`
		SolveRate   float64 `json:"SolveRate"`
	}

	// taskRowResponse: rates are 0..1, the median is seconds (null while
	// nobody solved the task), Calibration is ok, too_easy, too_hard,
	// insufficient or unknown.
	taskRowResponse struct {
		ExerciseID         uuid.UUID `json:"ExerciseID"`
		TaskID             uuid.UUID `json:"TaskID"`
		Exercise           string    `json:"Exercise"`
		Task               string    `json:"Task"`
		Level              string    `json:"Level"`
		Categories         []string  `json:"Categories"`
		EventsUsed         int64     `json:"EventsUsed"`
		Attempts           int64     `json:"Attempts"`
		TeamsTried         int64     `json:"TeamsTried"`
		TeamsEngaged       int64     `json:"TeamsEngaged"`
		Solves             int64     `json:"Solves"`
		TeamsHinted        int64     `json:"TeamsHinted"`
		SolveRate          float64   `json:"SolveRate"`
		HintRate           float64   `json:"HintRate"`
		MedianSolveSeconds *int64    `json:"MedianSolveSeconds"`
		Calibration        string    `json:"Calibration"`
	}

	categoryUsageResponse struct {
		Category string `json:"Category"`
		Tasks    int64  `json:"Tasks"`
		Uses     int64  `json:"Uses"`
	}

	levelSolveResponse struct {
		Level      string  `json:"Level"`
		Tasks      int64   `json:"Tasks"`
		TeamsTried int64   `json:"TeamsTried"`
		Solves     int64   `json:"Solves"`
		SolveRate  float64 `json:"SolveRate"`
	}
)

func toTaskRows(rows []platformAnalyticsUseCase.TaskRowView) []taskRowResponse {
	out := make([]taskRowResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, taskRowResponse(r))
	}
	return out
}

func toTasksResponse(v platformAnalyticsUseCase.TasksView) tasksResponse {
	out := tasksResponse{
		Period:        periodResponse{From: v.Period.From, To: v.Period.To, All: v.Period.All},
		Totals:        tasksTotalsResponse(v.Totals),
		Categories:    v.Categories,
		Levels:        v.Levels,
		Tasks:         toTaskRows(v.Tasks),
		TasksTotal:    v.TasksTotal,
		TasksLimit:    v.TasksLimit,
		Unsolved:      toTaskRows(v.Unsolved),
		UnsolvedTotal: v.UnsolvedTotal,
		ByCategory:    make([]categoryUsageResponse, 0, len(v.ByCategory)),
		ByLevel:       make([]levelSolveResponse, 0, len(v.ByLevel)),
	}
	for _, c := range v.ByCategory {
		out.ByCategory = append(out.ByCategory, categoryUsageResponse(c))
	}
	for _, l := range v.ByLevel {
		out.ByLevel = append(out.ByLevel, levelSolveResponse(l))
	}
	return out
}

// tasks godoc
// @Summary Platform analytics: catalog tasks across events
// @Description Usage and calibration of catalog tasks across the events that started in the period: events using each task, attempts, solves, solve rate (solvers ÷ teams that tried), median time to solve, hint unlock rate (teams that unlocked a hint ÷ teams that opened or tried), the used tasks nobody solved, usage by category and solve rate by level. Category is an exercise tag, level a task difficulty. The result is bounded (TasksLimit rows; TasksTotal is the full count). The moderators team is not counted.
// @Tags platform-analytics
// @Produce json
// @Param from query string false "period start (RFC 3339); omitted means all time"
// @Param to query string false "period end, exclusive (RFC 3339); omitted means now"
// @Param category query string false "exercise tag"
// @Param level query string false "difficulty: trivial, easy, medium, hard, insane"
// @Success 200 {object} response.Response{data=tasksResponse}
// @Router /analytics/tasks [get]
func (h *Handler) tasks(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetTasks(ctx, from, to, ctx.Query("category"), ctx.Query("level"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTasksResponse(v))
}

// exportTasks godoc
// @Summary Export a table of the catalog task analytics as CSV (UTF-8 with BOM)
// @Description table=tasks (usage and calibration) or table=unsolved (used tasks nobody solved). The category and level filters apply.
// @Tags platform-analytics
// @Produce text/csv
// @Param table query string true "tasks or unsolved"
// @Param from query string false "period start (RFC 3339); omitted means all time"
// @Param to query string false "period end, exclusive (RFC 3339); omitted means now"
// @Param category query string false "exercise tag"
// @Param level query string false "difficulty: trivial, easy, medium, hard, insane"
// @Success 200 {string} string "CSV"
// @Router /analytics/tasks/export.csv [get]
func (h *Handler) exportTasks(ctx *gin.Context) {
	table := ctx.Query("table")
	if table != "tasks" && table != "unsolved" {
		response.AbortWithError(ctx, platformAnalyticsModel.ErrPlatformAnalyticsTableUnknown.Err())
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetTasks(ctx, from, to, ctx.Query("category"), ctx.Query("level"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "tasks-"+table)
	rows := v.Tasks
	if table == "unsolved" {
		rows = v.Unsolved
	}
	_ = w.Write([]string{"Вправа", "Завдання", "Рівень", "Категорії", "Заходів", "Спроби", "Команд пробувало", "Команд відкривало або пробувало", "Розв'язань",
		"Частка розв'язань", "Медіана розв'язання (с)", "Команд із підказкою", "Частка підказок", "Калібрування"})
	for _, t := range rows {
		median := ""
		if t.MedianSolveSeconds != nil {
			median = csvInt(*t.MedianSolveSeconds)
		}
		_ = w.Write([]string{csvText(t.Exercise), csvText(t.Task), t.Level, csvText(strings.Join(t.Categories, ", ")), csvInt(t.EventsUsed), csvInt(t.Attempts),
			csvInt(t.TeamsTried), csvInt(t.TeamsEngaged), csvInt(t.Solves), csvFloat(t.SolveRate), median, csvInt(t.TeamsHinted), csvFloat(t.HintRate), t.Calibration})
	}
	w.Flush()
}
