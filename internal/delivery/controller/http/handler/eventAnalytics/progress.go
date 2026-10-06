package eventAnalytics

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// ProgressUseCase is the «Прогрес» part of IUseCase.
type ProgressUseCase interface {
	GetEventAnalyticsProgressScores(ctx context.Context, eventID uuid.UUID, from, to *time.Time, top int, teamIDs []uuid.UUID) (eventAnalyticsUseCase.ScoresView, error)
	GetEventAnalyticsProgressMatrix(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.MatrixView, error)
	GetEventAnalyticsProgressHeatmap(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.HeatmapView, error)
	GetEventAnalyticsProgressInactive(ctx context.Context, eventID uuid.UUID, minutes int) (eventAnalyticsUseCase.InactiveView, error)
}

func (h *Handler) initProgress(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("progress/scores", h.requireSections, h.progressScores)
	analytics.GET("progress/matrix", h.requireSections, h.progressMatrix)
	analytics.GET("progress/matrix/export.csv", h.requireSections, h.exportProgressMatrix)
	analytics.GET("progress/heatmap", h.requireSections, h.progressHeatmap)
	analytics.GET("progress/inactive", h.requireSections, h.progressInactive)
	analytics.GET("progress/inactive/export.csv", h.requireSections, h.exportProgressInactive)
}

type (
	scoresResponse struct {
		// Teams lists every team of the event for the picker.
		Teams  []scoreTeamResponse   `json:"Teams"`
		Series []scoreSeriesResponse `json:"Series"`
		Period periodResponse        `json:"Period"`
	}

	scoreTeamResponse struct {
		TeamID uuid.UUID `json:"TeamID"`
		Name   string    `json:"Name"`
		Points int64     `json:"Points"`
		Solved int64     `json:"Solved"`
		// Rank is the place among the ranked teams (0: hidden or not admitted).
		Rank     int64 `json:"Rank"`
		Hidden   bool  `json:"Hidden"`
		Admitted bool  `json:"Admitted"`
		Selected bool  `json:"Selected"`
	}

	// scoreSeriesResponse is a team's running score: a point at the period
	// start, one per score change, one at the end.
	scoreSeriesResponse struct {
		TeamID uuid.UUID            `json:"TeamID"`
		Name   string               `json:"Name"`
		Points []scorePointResponse `json:"Points"`
	}

	scorePointResponse struct {
		At    time.Time `json:"At"`
		Score int64     `json:"Score"`
	}

	// matrixResponse lists only the touched cells: a cell with SolvedAt is
	// solved, one without is tried, the rest are untouched.
	matrixResponse struct {
		Tasks  []matrixTaskResponse `json:"Tasks"`
		Teams  []matrixTeamResponse `json:"Teams"`
		Cells  []matrixCellResponse `json:"Cells"`
		Period periodResponse       `json:"Period"`
	}

	matrixTaskResponse struct {
		ChallengeID uuid.UUID `json:"ChallengeID"`
		Name        string    `json:"Name"`
		GroupName   string    `json:"GroupName"`
	}

	matrixTeamResponse struct {
		TeamID uuid.UUID `json:"TeamID"`
		Name   string    `json:"Name"`
		Points int64     `json:"Points"`
		Solved int64     `json:"Solved"`
	}

	matrixCellResponse struct {
		TeamID      uuid.UUID  `json:"TeamID"`
		ChallengeID uuid.UUID  `json:"ChallengeID"`
		Attempts    int64      `json:"Attempts"`
		SolvedAt    *time.Time `json:"SolvedAt"`
	}

	// heatmapResponse: Hours is every hour of the period, Cells the non-empty
	// team × hour pairs; Activity is attempts + opens + solves.
	heatmapResponse struct {
		Hours       []time.Time          `json:"Hours"`
		Teams       []matrixTeamResponse `json:"Teams"`
		Cells       []heatCellResponse   `json:"Cells"`
		MaxActivity int64                `json:"MaxActivity"`
		Period      periodResponse       `json:"Period"`
		RefreshedAt *time.Time           `json:"RefreshedAt"`
		Final       bool                 `json:"Final"`
	}

	heatCellResponse struct {
		TeamID   uuid.UUID `json:"TeamID"`
		HourAt   time.Time `json:"HourAt"`
		Attempts int64     `json:"Attempts"`
		Opens    int64     `json:"Opens"`
		Solves   int64     `json:"Solves"`
		Activity int64     `json:"Activity"`
	}

	// inactiveResponse lists the ranked teams idle for longer than Minutes as
	// of AsOf (now while the event runs, the finish once it is over).
	inactiveResponse struct {
		Minutes int64                  `json:"Minutes"`
		AsOf    time.Time              `json:"AsOf"`
		Running bool                   `json:"Running"`
		Teams   []inactiveTeamResponse `json:"Teams"`
	}

	inactiveTeamResponse struct {
		TeamID         uuid.UUID  `json:"TeamID"`
		Name           string     `json:"Name"`
		LastActivityAt *time.Time `json:"LastActivityAt"`
		IdleMinutes    int64      `json:"IdleMinutes"`
		Points         int64      `json:"Points"`
	}
)

// progressScores godoc
// @Summary Score over time of the top teams and the chosen ones
// @Description §6.4. The running total follows the scoreboard (hint costs included). Each line has a point at the period start, one per score change and one at the end.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Param top query int false "how many leading ranked teams to draw (default 10, max 50)"
// @Param teams query string false "team IDs to add, comma separated (max 20)"
// @Success 200 {object} response.Response{data=scoresResponse}
// @Router /events/{id}/manage/analytics/progress/scores [get]
func (h *Handler) progressScores(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	top := eventAnalyticsUseCase.DefaultScoreTop
	if raw := ctx.Query("top"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			response.AbortWithBadRequest(ctx, errors.New("top must be a non-negative integer"))
			return
		}
		top = parsed
	}
	teamIDs, ok := parseTeamIDs(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsProgressScores(ctx, eventID, from, to, top, teamIDs)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	teams := make([]scoreTeamResponse, 0, len(v.Teams))
	for _, t := range v.Teams {
		teams = append(teams, scoreTeamResponse{TeamID: t.TeamID, Name: t.Name, Points: t.Points, Solved: t.Solved, Rank: t.Rank, Hidden: t.Hidden, Admitted: t.Admitted, Selected: t.Selected})
	}
	series := make([]scoreSeriesResponse, 0, len(v.Series))
	for _, s := range v.Series {
		points := make([]scorePointResponse, 0, len(s.Points))
		for _, p := range s.Points {
			points = append(points, scorePointResponse{At: p.At, Score: p.Score})
		}
		series = append(series, scoreSeriesResponse{TeamID: s.TeamID, Name: s.Name, Points: points})
	}
	response.AbortWithData(ctx, scoresResponse{Teams: teams, Series: series, Period: periodResponse{From: v.Period.From, To: v.Period.To}})
}

// parseTeamIDs reads the teams query parameter: comma separated IDs, the
// parameter may repeat.
func parseTeamIDs(ctx *gin.Context) ([]uuid.UUID, bool) {
	var ids []uuid.UUID
	for _, raw := range ctx.QueryArray("teams") {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := uuid.FromString(part)
			if err != nil {
				response.AbortWithBadRequest(ctx, err)
				return nil, false
			}
			ids = append(ids, id)
		}
	}
	return ids, true
}

// progressMatrix godoc
// @Summary Team × task matrix
// @Description §6.4. Cells that were touched in the period: solved (with the time) or tried (with the attempts); untouched cells are omitted. Teams are the ranked ones, in ranking order.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=matrixResponse}
// @Router /events/{id}/manage/analytics/progress/matrix [get]
func (h *Handler) progressMatrix(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsProgressMatrix(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toMatrixResponse(v))
}

func toMatrixResponse(v eventAnalyticsUseCase.MatrixView) matrixResponse {
	tasks := make([]matrixTaskResponse, 0, len(v.Tasks))
	for _, t := range v.Tasks {
		tasks = append(tasks, matrixTaskResponse{ChallengeID: t.ChallengeID, Name: t.Name, GroupName: t.GroupName})
	}
	cells := make([]matrixCellResponse, 0, len(v.Cells))
	for _, c := range v.Cells {
		cells = append(cells, matrixCellResponse{TeamID: c.TeamID, ChallengeID: c.ChallengeID, Attempts: c.Attempts, SolvedAt: c.SolvedAt})
	}
	return matrixResponse{Tasks: tasks, Teams: toMatrixTeams(v.Teams), Cells: cells, Period: periodResponse{From: v.Period.From, To: v.Period.To}}
}

func toMatrixTeams(teams []eventAnalyticsUseCase.MatrixTeamView) []matrixTeamResponse {
	out := make([]matrixTeamResponse, 0, len(teams))
	for _, t := range teams {
		out = append(out, matrixTeamResponse{TeamID: t.TeamID, Name: t.Name, Points: t.Points, Solved: t.Solved})
	}
	return out
}

// exportProgressMatrix godoc
// @Summary Export the team × task matrix as CSV (UTF-8 with BOM)
// @Description One row per team, one column per task: the solve time (UTC), "спроб: N" for tried, empty for untouched.
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/progress/matrix/export.csv [get]
func (h *Handler) exportProgressMatrix(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsProgressMatrix(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	type key struct{ team, task uuid.UUID }
	cells := make(map[key]eventAnalyticsUseCase.MatrixCellView, len(v.Cells))
	for _, c := range v.Cells {
		cells[key{c.TeamID, c.ChallengeID}] = c
	}
	w := startAnalyticsCSV(ctx, "matrix")
	header := []string{"Команда", "Бали", "Розвʼязано"}
	for _, t := range v.Tasks {
		header = append(header, csvCellText(t.Name))
	}
	_ = w.Write(header)
	for _, team := range v.Teams {
		row := []string{csvCellText(team.Name), strconv.FormatInt(team.Points, 10), strconv.FormatInt(team.Solved, 10)}
		for _, t := range v.Tasks {
			c, touched := cells[key{team.TeamID, t.ChallengeID}]
			switch {
			case !touched:
				row = append(row, "")
			case c.SolvedAt != nil:
				row = append(row, c.SolvedAt.UTC().Format(time.RFC3339))
			default:
				row = append(row, "спроб: "+strconv.FormatInt(c.Attempts, 10))
			}
		}
		_ = w.Write(row)
	}
	w.Flush()
}

// progressHeatmap godoc
// @Summary Team × hour activity heatmap
// @Description §6.4, from the 5-minute rollup: attempts, task opens and solves per team and hour. Only non-empty cells are listed.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=heatmapResponse}
// @Router /events/{id}/manage/analytics/progress/heatmap [get]
func (h *Handler) progressHeatmap(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsProgressHeatmap(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	cells := make([]heatCellResponse, 0, len(v.Cells))
	for _, c := range v.Cells {
		cells = append(cells, heatCellResponse{TeamID: c.TeamID, HourAt: c.HourAt, Attempts: c.Attempts, Opens: c.Opens, Solves: c.Solves, Activity: c.Activity})
	}
	hours := v.Hours
	if hours == nil {
		hours = []time.Time{}
	}
	response.AbortWithData(ctx, heatmapResponse{
		Hours: hours, Teams: toMatrixTeams(v.Teams), Cells: cells, MaxActivity: v.MaxActivity,
		Period: periodResponse{From: v.Period.From, To: v.Period.To}, RefreshedAt: v.RefreshedAt, Final: v.Final,
	})
}

// progressInactive godoc
// @Summary Teams with no activity for longer than N minutes
// @Description §6.4. Activity is an attempt, a task open or download, or a hint unlock. Measured now while the event runs, at the finish once it is over; nobody is idle before the start.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param minutes query int false "idle threshold in minutes (default 30, 5..1440)"
// @Success 200 {object} response.Response{data=inactiveResponse}
// @Router /events/{id}/manage/analytics/progress/inactive [get]
func (h *Handler) progressInactive(ctx *gin.Context) {
	v, ok := h.inactive(ctx)
	if !ok {
		return
	}
	teams := make([]inactiveTeamResponse, 0, len(v.Teams))
	for _, t := range v.Teams {
		teams = append(teams, inactiveTeamResponse{TeamID: t.TeamID, Name: t.Name, LastActivityAt: t.LastActivityAt, IdleMinutes: t.IdleMinutes, Points: t.Points})
	}
	response.AbortWithData(ctx, inactiveResponse{Minutes: v.Minutes, AsOf: v.AsOf, Running: v.Running, Teams: teams})
}

// exportProgressInactive godoc
// @Summary Export the inactive teams as CSV (UTF-8 with BOM)
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param minutes query int false "idle threshold in minutes (default 30, 5..1440)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/progress/inactive/export.csv [get]
func (h *Handler) exportProgressInactive(ctx *gin.Context) {
	v, ok := h.inactive(ctx)
	if !ok {
		return
	}
	w := startAnalyticsCSV(ctx, "inactive")
	_ = w.Write([]string{"Команда", "Бали", "Остання активність (UTC)", "Простій (хв)"})
	for _, t := range v.Teams {
		_ = w.Write([]string{csvCellText(t.Name), strconv.FormatInt(t.Points, 10), csvTimePtr(t.LastActivityAt), strconv.FormatInt(t.IdleMinutes, 10)})
	}
	w.Flush()
}

func (h *Handler) inactive(ctx *gin.Context) (eventAnalyticsUseCase.InactiveView, bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return eventAnalyticsUseCase.InactiveView{}, false
	}
	minutes := eventAnalyticsUseCase.DefaultInactiveMinutes
	if raw := ctx.Query("minutes"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return eventAnalyticsUseCase.InactiveView{}, false
		}
		minutes = parsed
	}
	v, err := h.useCase.GetEventAnalyticsProgressInactive(ctx, eventID, minutes)
	if err != nil {
		response.AbortWithError(ctx, err)
		return eventAnalyticsUseCase.InactiveView{}, false
	}
	return v, true
}
