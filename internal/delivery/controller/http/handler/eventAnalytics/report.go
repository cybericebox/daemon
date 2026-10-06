package eventAnalytics

import (
	"archive/zip"
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
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// ReportUseCase is the «Звіт по заході» part of IUseCase.
type ReportUseCase interface {
	GetEventAnalyticsReport(ctx context.Context, eventID uuid.UUID) (eventAnalyticsUseCase.ReportView, error)
}

// initReport registers the report routes. The report has no answer texts, so
// every analytics viewer may read and download it (§7). There is no PDF
// route: the backend has no PDF renderer, so the frontend serves a
// print-optimised page and the browser saves it as PDF.
func (h *Handler) initReport(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("report", h.requireSections, h.report)
	analytics.GET("report/export.zip", h.requireSections, h.exportReport)
}

type (
	// reportResponse: before the finish Available is false and only the
	// event name and dates are set.
	reportResponse struct {
		Available   bool       `json:"Available"`
		EventName   string     `json:"EventName"`
		StartAt     time.Time  `json:"StartAt"`
		FinishAt    *time.Time `json:"FinishAt"`
		GeneratedAt time.Time  `json:"GeneratedAt"`

		Participants participantCountsResponse `json:"Participants"`
		Teams        teamCountsResponse        `json:"Teams"`
		Tasks        int64                     `json:"Tasks"`
		Attempts     int64                     `json:"Attempts"`
		// Correct counts effectively correct attempts (after decisions).
		Correct     int64 `json:"Correct"`
		Solves      int64 `json:"Solves"`
		HintsOpened int64 `json:"HintsOpened"`
		HintPoints  int64 `json:"HintPoints"`

		Ranking  []reportRankResponse `json:"Ranking"`
		TaskRows []reportTaskResponse `json:"TaskRows"`
		// The funnels' Key values: registered, approved, opened, attempted
		// (participants); teams, admitted, tried, solved (teams).
		ParticipantFunnel []funnelStepResponse  `json:"ParticipantFunnel"`
		TeamFunnel        []funnelStepResponse  `json:"TeamFunnel"`
		Series            []seriesPointResponse `json:"Series"`
		// Stands is set for an event with infrastructure.
		Stands *standsSummaryResponse `json:"Stands"`
	}

	// reportRankResponse: equal scores share a Rank.
	reportRankResponse struct {
		Rank        int        `json:"Rank"`
		TeamID      uuid.UUID  `json:"TeamID"`
		Name        string     `json:"Name"`
		Individual  bool       `json:"Individual"`
		Members     int64      `json:"Members"`
		Points      int64      `json:"Points"`
		Solved      int64      `json:"Solved"`
		Attempts    int64      `json:"Attempts"`
		LastSolveAt *time.Time `json:"LastSolveAt"`
	}

	// reportTaskResponse: SolveRate is solves over the teams that tried (0..1).
	reportTaskResponse struct {
		ChallengeID     uuid.UUID  `json:"ChallengeID"`
		Name            string     `json:"Name"`
		Points          int64      `json:"Points"`
		TeamsOpened     int64      `json:"TeamsOpened"`
		TeamsAttempted  int64      `json:"TeamsAttempted"`
		Attempts        int64      `json:"Attempts"`
		CorrectAttempts int64      `json:"CorrectAttempts"`
		Solves          int64      `json:"Solves"`
		SolveRate       float64    `json:"SolveRate"`
		HintsUnlocked   int64      `json:"HintsUnlocked"`
		FirstSolveAt    *time.Time `json:"FirstSolveAt"`
		FirstSolveTeam  string     `json:"FirstSolveTeam"`
	}

	funnelStepResponse struct {
		Key   string `json:"Key"`
		Count int64  `json:"Count"`
	}
)

// report godoc
// @Summary The event report (after the finish)
// @Description §6.8: key numbers, the final ranking, per-task statistics, the participation funnels, the activity series and (with infrastructure) the stands summary. Before the finish Available is false.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=reportResponse}
// @Router /events/{id}/manage/analytics/report [get]
func (h *Handler) report(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsReport(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toReportResponse(v))
}

func toReportResponse(v eventAnalyticsUseCase.ReportView) reportResponse {
	out := reportResponse{
		Available: v.Available, EventName: v.EventName, StartAt: v.StartAt, FinishAt: v.FinishAt, GeneratedAt: v.GeneratedAt,
		Participants: participantCountsResponse{
			Registered: v.Participants.Registered, Approved: v.Participants.Approved, Pending: v.Participants.Pending,
			Invited: v.Participants.Invited, Active: v.Participants.Active,
		},
		Teams: teamCountsResponse{Total: v.Teams.Total, Admitted: v.Teams.Admitted, Incomplete: v.Teams.Incomplete},
		Tasks: v.Tasks, Attempts: v.Attempts, Correct: v.Correct, Solves: v.Solves, HintsOpened: v.HintsOpened, HintPoints: v.HintPoints,
		Ranking:           make([]reportRankResponse, 0, len(v.Ranking)),
		TaskRows:          make([]reportTaskResponse, 0, len(v.TaskRows)),
		ParticipantFunnel: funnelResponse(v.ParticipantFunnel),
		TeamFunnel:        funnelResponse(v.TeamFunnel),
		Series:            make([]seriesPointResponse, 0, len(v.Series)),
	}
	for _, r := range v.Ranking {
		out.Ranking = append(out.Ranking, reportRankResponse{
			Rank: r.Rank, TeamID: r.TeamID, Name: r.Name, Individual: r.Individual, Members: r.Members,
			Points: r.Points, Solved: r.Solved, Attempts: r.Attempts, LastSolveAt: r.LastSolveAt,
		})
	}
	for _, t := range v.TaskRows {
		out.TaskRows = append(out.TaskRows, reportTaskResponse{
			ChallengeID: t.ChallengeID, Name: t.Name, Points: t.Points, TeamsOpened: t.TeamsOpened, TeamsAttempted: t.TeamsAttempted,
			Attempts: t.Attempts, CorrectAttempts: t.CorrectAttempts, Solves: t.Solves, SolveRate: t.SolveRate,
			HintsUnlocked: t.HintsUnlocked, FirstSolveAt: t.FirstSolveAt, FirstSolveTeam: t.FirstSolveTeam,
		})
	}
	for _, p := range v.Series {
		out.Series = append(out.Series, seriesPointResponse{At: p.At, Attempts: p.Attempts, Correct: p.Correct, Solves: p.Solves, Opens: p.Opens})
	}
	if v.Stands != nil {
		stands := toStandsResponse(eventAnalyticsUseCase.StandsView{Summary: *v.Stands}).Summary
		out.Stands = &stands
	}
	return out
}

func funnelResponse(steps []eventAnalyticsUseCase.FunnelStepView) []funnelStepResponse {
	out := make([]funnelStepResponse, 0, len(steps))
	for _, s := range steps {
		out = append(out, funnelStepResponse{Key: s.Key, Count: s.Count})
	}
	return out
}

// exportReport godoc
// @Summary The event report as a ZIP of CSV tables (UTF-8 with BOM)
// @Description summary.csv, ranking.csv, tasks.csv, funnel.csv and activity.csv. Only after the finish.
// @Tags event-analytics
// @Produce application/zip
// @Param id path string true "event ID"
// @Success 200 {file} file "ZIP"
// @Router /events/{id}/manage/analytics/report/export.zip [get]
func (h *Handler) exportReport(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsReport(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if !v.Available {
		response.AbortWithError(ctx, eventAnalyticsModel.ErrEventAnalyticsReportNotReady.Err())
		return
	}
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Type", "application/zip")
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="analytics-report-%s.zip"`, time.Now().UTC().Format("20060102-1504")))
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Status(http.StatusOK)
	zw := zip.NewWriter(ctx.Writer)
	if err := writeReportTables(zw, v); err != nil {
		// The download has started: nothing sensible is left but to abort it.
		_ = ctx.Error(err)
		ctx.Abort()
		return
	}
	_ = zw.Close()
}

// writeReportTables writes the report's CSV tables, each with the UTF-8 BOM
// spreadsheets need for Cyrillic.
func writeReportTables(zw *zip.Writer, v eventAnalyticsUseCase.ReportView) error {
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	table := func(name string, header []string, rows [][]string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err = f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
			return err
		}
		w := csv.NewWriter(f)
		_ = w.Write(header)
		_ = w.WriteAll(rows)
		w.Flush()
		return w.Error()
	}

	summary := [][]string{
		{"Захід", csvCellText(v.EventName)},
		{"Початок (UTC)", csvTimePtr(&v.StartAt)},
		{"Завершення (UTC)", csvTimePtr(v.FinishAt)},
		{"Зареєстровано учасників", n(v.Participants.Registered)},
		{"Схвалено учасників", n(v.Participants.Approved)},
		{"Команд", n(v.Teams.Total)},
		{"Допущено команд", n(v.Teams.Admitted)},
		{"Завдань", n(v.Tasks)},
		{"Спроб", n(v.Attempts)},
		{"Правильних спроб", n(v.Correct)},
		{"Розвʼязань", n(v.Solves)},
		{"Відкрито підказок", n(v.HintsOpened)},
		{"Витрачено балів на підказки", n(v.HintPoints)},
	}
	if v.Stands != nil {
		summary = append(summary,
			[]string{"Стендів готово", n(v.Stands.Ready)},
			[]string{"Стендів зі збоєм", n(v.Stands.Failed)},
			[]string{"Збоїв стендів і лабораторій", n(v.Stands.Failures)},
			[]string{"Середнє розгортання стенда (с)", csvSeconds(v.Stands.DeployAvg)})
	}
	if err := table("summary.csv", []string{"Показник", "Значення"}, summary); err != nil {
		return err
	}

	ranking := make([][]string, 0, len(v.Ranking))
	for _, r := range v.Ranking {
		ranking = append(ranking, []string{strconv.Itoa(r.Rank), csvCellText(r.Name), n(r.Members), n(r.Points), n(r.Solved), n(r.Attempts), csvTimePtr(r.LastSolveAt)})
	}
	if err := table("ranking.csv", []string{"Місце", "Команда", "Учасників", "Бали", "Розвʼязано", "Спроб", "Останнє розвʼязання (UTC)"}, ranking); err != nil {
		return err
	}

	tasks := make([][]string, 0, len(v.TaskRows))
	for _, t := range v.TaskRows {
		tasks = append(tasks, []string{csvCellText(t.Name), n(t.Points), n(t.TeamsOpened), n(t.TeamsAttempted), n(t.Attempts), n(t.CorrectAttempts),
			n(t.Solves), csvRate(t.SolveRate), n(t.HintsUnlocked), csvCellText(t.FirstSolveTeam), csvTimePtr(t.FirstSolveAt)})
	}
	if err := table("tasks.csv", []string{"Завдання", "Бали", "Команд відкривало", "Команд пробувало", "Спроб", "Правильних спроб", "Розвʼязань",
		"Частка розвʼязань", "Підказок", "Перша кров", "Час першої крові (UTC)"}, tasks); err != nil {
		return err
	}

	funnel := make([][]string, 0, len(v.ParticipantFunnel)+len(v.TeamFunnel))
	for _, s := range v.ParticipantFunnel {
		funnel = append(funnel, []string{"participants", s.Key, n(s.Count)})
	}
	for _, s := range v.TeamFunnel {
		funnel = append(funnel, []string{"teams", s.Key, n(s.Count)})
	}
	if err := table("funnel.csv", []string{"Група", "Крок", "Кількість"}, funnel); err != nil {
		return err
	}

	activity := make([][]string, 0, len(v.Series))
	for _, p := range v.Series {
		if p.Attempts == 0 && p.Solves == 0 && p.Opens == 0 {
			continue
		}
		at := p.At
		activity = append(activity, []string{csvTimePtr(&at), n(p.Attempts), n(p.Correct), n(p.Solves), n(p.Opens)})
	}
	return table("activity.csv", []string{"Інтервал 5 хв (UTC)", "Спроб", "Правильних спроб", "Розвʼязань", "Відкриттів завдань"}, activity)
}
