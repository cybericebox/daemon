package event

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/sse"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/pagination"
	"github.com/cybericebox/daemon/pkg/tools"
)

type resultsFreezeStateResponse struct {
	Enabled  bool       `json:"Enabled"`
	FrozenAt *time.Time `json:"FrozenAt"`
	FinishAt *time.Time `json:"FinishAt"`
	OpenedAt *time.Time `json:"OpenedAt"`
	Active   bool       `json:"Active"`
}

func toResultsFreezeStateResponse(v eventUseCase.ResultsFreezeView) resultsFreezeStateResponse {
	return resultsFreezeStateResponse{Enabled: v.Enabled, FrozenAt: v.FrozenAt, FinishAt: v.FinishAt, OpenedAt: v.OpenedAt, Active: v.Active}
}

type manageResultsTeamResponse struct {
	Rank       *int32    `json:"Rank"`
	TeamID     uuid.UUID `json:"TeamID"`
	Name       string    `json:"Name"`
	RealName   string    `json:"RealName"`
	Pseudonym  *string   `json:"Pseudonym"`
	Individual bool      `json:"Individual"`
	Hidden     bool      `json:"Hidden"`
	// Moderators marks the always hidden team of the event managers (no name).
	Moderators  bool       `json:"Moderators"`
	Admitted    bool       `json:"Admitted"`
	Points      int64      `json:"Points"`
	Solved      int64      `json:"Solved"`
	LastSolveAt *time.Time `json:"LastSolveAt"`
	Hints       int64      `json:"Hints"`
	HintPoints  int64      `json:"HintPoints"`
	// Solves are the team's solved challenges in solve order.
	Solves []manageResultsSolveResponse `json:"Solves"`
}

type manageResultsSolveResponse struct {
	ChallengeID   uuid.UUID `json:"ChallengeID"`
	ChallengeName string    `json:"ChallengeName"`
	Points        int32     `json:"Points"`
	SolvedAt      time.Time `json:"SolvedAt"`
	FirstBlood    bool      `json:"FirstBlood"`
}

type manageResultsCountsResponse struct {
	Ranked      int `json:"Ranked"`
	Hidden      int `json:"Hidden"`
	NotAdmitted int `json:"NotAdmitted"`
}

type manageResultsResponse struct {
	Revision    int64                       `json:"Revision"`
	GeneratedAt time.Time                   `json:"GeneratedAt"`
	Freeze      resultsFreezeStateResponse  `json:"Freeze"`
	Counts      manageResultsCountsResponse `json:"Counts"`
	Teams       []manageResultsTeamResponse `json:"Teams"`
}

type resultsSettingsRequest struct {
	ScoreboardVisibility eventConfigModel.Visibility `json:"ScoreboardVisibility"`
	FreezeEnabled        bool                        `json:"FreezeEnabled"`
	FreezeMinutes        int32                       `json:"FreezeMinutes"`
	// LiveFreeze is optional: omitted keeps the current value (W9 owns its UI).
	LiveFreeze   *bool  `json:"LiveFreeze"`
	ChartEnabled bool   `json:"ChartEnabled"`
	ChartTeams   int32  `json:"ChartTeams"`
	RowsLimit    *int32 `json:"RowsLimit"`
}

type resultsSettingsResponse struct {
	ScoreboardVisibility eventConfigModel.Visibility `json:"ScoreboardVisibility"`
	FreezeEnabled        bool                        `json:"FreezeEnabled"`
	FreezeMinutes        int32                       `json:"FreezeMinutes"`
	LiveFreeze           bool                        `json:"LiveFreeze"`
	ChartEnabled         bool                        `json:"ChartEnabled"`
	ChartTeams           int32                       `json:"ChartTeams"`
	RowsLimit            *int32                      `json:"RowsLimit"`
	OpenedAt             *time.Time                  `json:"OpenedAt"`
	Freeze               resultsFreezeStateResponse  `json:"Freeze"`
}

func toResultsSettingsResponse(v eventUseCase.ResultsSettingsView) resultsSettingsResponse {
	return resultsSettingsResponse{ScoreboardVisibility: v.ScoreboardVisibility, FreezeEnabled: v.FreezeEnabled, FreezeMinutes: v.FreezeMinutes, LiveFreeze: v.LiveFreeze,
		ChartEnabled: v.ChartEnabled, ChartTeams: v.ChartTeams, RowsLimit: v.RowsLimit, OpenedAt: v.OpenedAt, Freeze: toResultsFreezeStateResponse(v.Freeze)}
}

type resultsOpenedRequest struct {
	Opened bool `json:"Opened"`
}

type annulSolveRequest struct {
	TeamID      uuid.UUID `json:"TeamID" binding:"required"`
	ChallengeID uuid.UUID `json:"ChallengeID" binding:"required"`
	Reason      string    `json:"Reason"`
}

type annulSolveResponse struct {
	TeamID      uuid.UUID `json:"TeamID"`
	ChallengeID uuid.UUID `json:"ChallengeID"`
	Rejected    int       `json:"Rejected"`
}

// getManageResults godoc
// @Summary Moderators' live results: every team, hidden and not admitted ones marked
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=manageResultsResponse}
// @Router /events/{id}/manage/results [get]
func (h *Handler) getManageResults(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetManageResults(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := manageResultsResponse{Revision: v.Revision, GeneratedAt: v.GeneratedAt, Freeze: toResultsFreezeStateResponse(v.Freeze),
		Counts: manageResultsCountsResponse{Ranked: v.Counts.Ranked, Hidden: v.Counts.Hidden, NotAdmitted: v.Counts.NotAdmitted}, Teams: make([]manageResultsTeamResponse, 0, len(v.Teams))}
	for _, team := range v.Teams {
		out.Teams = append(out.Teams, toManageResultsTeamResponse(team))
	}
	response.AbortWithData(ctx, out)
}

func toManageResultsTeamResponse(team eventUseCase.ManageResultsTeamView) manageResultsTeamResponse {
	solves := make([]manageResultsSolveResponse, 0, len(team.Solves))
	for _, solve := range team.Solves {
		solves = append(solves, manageResultsSolveResponse{ChallengeID: solve.ChallengeID, ChallengeName: solve.ChallengeName, Points: solve.Points, SolvedAt: solve.SolvedAt, FirstBlood: solve.FirstBlood})
	}
	return manageResultsTeamResponse{Rank: team.Rank, TeamID: team.TeamID, Name: team.Name, RealName: team.RealName, Pseudonym: team.Pseudonym,
		Individual: team.Individual, Hidden: team.Hidden, Moderators: team.Moderators, Admitted: team.Admitted, Points: team.Points, Solved: team.Solved, LastSolveAt: team.LastSolveAt,
		Hints: team.Hints, HintPoints: team.HintPoints, Solves: solves}
}

// exportManageResults godoc
// @Summary Export the moderators' live results as CSV (UTF-8 with BOM)
// @Tags events
// @Produce text/csv
// @Param id path string true "event ID"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/results/export.csv [get]
func (h *Handler) exportManageResults(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetManageResults(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "results")
	_ = w.Write([]string{"Місце", "Назва", "Справжнє імʼя", "Псевдонім", "Бали", "Розвʼязано", "Останнє розвʼязання (UTC)", "Прихована", "Допущена"})
	for _, team := range v.Teams {
		if team.Moderators {
			continue
		}
		rank, pseudonym := "", ""
		if team.Rank != nil {
			rank = strconv.Itoa(int(*team.Rank))
		}
		if team.Pseudonym != nil {
			pseudonym = *team.Pseudonym
		}
		_ = w.Write([]string{rank, csvText(team.Name), csvText(team.RealName), csvText(pseudonym), strconv.FormatInt(team.Points, 10), strconv.FormatInt(team.Solved, 10),
			csvTime(team.LastSolveAt), yesNo(team.Hidden), yesNo(team.Admitted)})
	}
	w.Flush()
}

// getResultsSettings godoc
// @Summary Read the results page settings and the freeze state
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=resultsSettingsResponse}
// @Router /events/{id}/manage/results-settings [get]
func (h *Handler) getResultsSettings(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetResultsSettings(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResultsSettingsResponse(v))
}

// updateResultsSettings godoc
// @Summary Save the results visibility, freeze, chart and rows settings
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body resultsSettingsRequest true "results settings"
// @Success 200 {object} response.Response{data=resultsSettingsResponse}
// @Router /events/{id}/manage/results-settings [put]
func (h *Handler) updateResultsSettings(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req resultsSettingsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	liveFreeze := true
	if req.LiveFreeze != nil {
		liveFreeze = *req.LiveFreeze
	} else if current, err := h.useCase.GetResultsSettings(ctx, eventID); err == nil {
		liveFreeze = current.LiveFreeze
	}
	in := eventConfigModel.ResultsSettings{FreezeEnabled: req.FreezeEnabled, FreezeMinutes: req.FreezeMinutes, LiveFreeze: liveFreeze,
		ChartEnabled: req.ChartEnabled, ChartTeams: req.ChartTeams, RowsLimit: req.RowsLimit}
	v, err := h.useCase.UpdateResultsSettings(ctx, eventID, req.ScoreboardVisibility, in, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResultsSettingsResponse(v))
}

// setResultsOpened godoc
// @Summary Open the results before the finish («Відкрити підсумки») or restore the freeze
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body resultsOpenedRequest true "opened"
// @Success 200 {object} response.Response{data=resultsSettingsResponse}
// @Router /events/{id}/manage/results/opened [put]
func (h *Handler) setResultsOpened(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req resultsOpenedRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.SetResultsOpened(ctx, eventID, req.Opened, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResultsSettingsResponse(v))
}

// annulSolve godoc
// @Summary Annul a team's solve of one challenge: reject every accepted attempt in one transaction
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body annulSolveRequest true "team, event challenge and required reason"
// @Success 200 {object} response.Response{data=annulSolveResponse}
// @Router /events/{id}/manage/solution-attempts/annul [post]
func (h *Handler) annulSolve(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req annulSolveRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.AnnulSolve(ctx, eventID, req.TeamID, req.ChallengeID, req.Reason, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, annulSolveResponse{TeamID: v.TeamID, ChallengeID: v.ChallengeID, Rejected: v.Rejected})
}

// exportSolutionAttempts godoc
// @Summary Export the filtered attempts journal as CSV (UTF-8 with BOM)
// @Tags events
// @Produce text/csv
// @Param id path string true "event ID"
// @Param teamId query string false "team ID"
// @Param participantId query string false "participant ID"
// @Param challengeId query string false "event challenge ID"
// @Param correct query bool false "correctness"
// @Param from query string false "RFC3339 inclusive lower time bound"
// @Param to query string false "RFC3339 exclusive upper time bound"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/solution-attempts/export.csv [get]
func (h *Handler) exportSolutionAttempts(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	filter, err := solutionAttemptsFilter(ctx, eventID)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	filter.PageSize = pagination.MaxPageSize
	// The first page is read before the header so an error is still JSON.
	page, err := h.useCase.ListSolutionAttempts(ctx, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "attempts")
	_ = w.Write([]string{"Час (UTC)", "Завдання", "Команда", "Учасник", "Відповідь", "Автоперевірка", "Рішення", "Зараховано", "Причина рішення"})
	for {
		for _, item := range page.Items {
			reason := ""
			if item.DecisionReason != nil {
				reason = *item.DecisionReason
			}
			_ = w.Write([]string{item.ReceivedAt.UTC().Format(time.RFC3339), csvText(item.ChallengeName), csvText(item.TeamName), csvText(item.ParticipantName), csvText(item.Answer),
				yesNo(item.AutomaticCorrect), item.Decision.String(), yesNo(item.Correct), csvText(reason)})
		}
		if !page.HasMore {
			break
		}
		filter.Cursor = page.NextCursor
		if page, err = h.useCase.ListSolutionAttempts(ctx, filter); err != nil {
			// Headers are sent: end the file visibly incomplete.
			_ = w.Write([]string{"# export interrupted"})
			break
		}
	}
	w.Flush()
}

// maxJournalStreamsPerUser bounds the open attempts-journal streams of one account.
const maxJournalStreamsPerUser = 6

// liveSolutionAttempts godoc
// @Summary Stream attempts journal changes (new attempts or decisions)
// @Tags events
// @Produce text/event-stream
// @Param id path string true "event ID"
// @Param pollInterval query integer false "Database polling interval in seconds (2-30, default 3)"
// @Success 200 {string} string "SSE attempts-changed events"
// @Router /events/{id}/manage/solution-attempts/live [get]
func (h *Handler) liveSolutionAttempts(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	interval := 3 * time.Second
	if value := ctx.Query("pollInterval"); value != "" {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds < 2 || seconds > 30 {
			response.AbortWithBadRequest(ctx, fmt.Errorf("pollInterval must be between 2 and 30 seconds"))
			return
		}
		interval = time.Duration(seconds) * time.Second
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	// One account keeps only a few journal streams open, and the stream ends when the reader
	// loses the right to read the event (the gate ran once, at the start).
	release, ok := sse.Streams.Acquire("attempts:"+claims.UserID.String(), maxJournalStreamsPerUser)
	if !ok {
		response.AbortWithTooManyRequests(ctx)
		return
	}
	defer release()
	flusher, streamCtx, cancel, err := sse.Open(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer cancel()
	stamp, err := h.useCase.GetSolutionAttemptsStamp(streamCtx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	sse.WriteHeaders(ctx)
	// attempts-changed covers attempts and decisions; hints-changed covers
	// opened hints (the «Відкриті підказки» view).
	write := func(value eventUseCase.SolutionAttemptsStampView) {
		payload, _ := json.Marshal(struct{ Attempts, Decisions int64 }{value.Attempts, value.Decisions})
		_, _ = fmt.Fprintf(ctx.Writer, "event: attempts-changed\ndata: %s\n\n", payload)
		flusher.Flush()
	}
	writeHints := func(value eventUseCase.SolutionAttemptsStampView) {
		payload, _ := json.Marshal(struct{ HintUnlocks int64 }{value.HintUnlocks})
		_, _ = fmt.Fprintf(ctx.Writer, "event: hints-changed\ndata: %s\n\n", payload)
		flusher.Flush()
	}
	write(stamp)
	writeHints(stamp)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(sse.HeartbeatInterval)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(sse.RevalidateInterval)
	defer revalidate.Stop()
	for {
		select {
		case <-streamCtx.Done():
			return
		case <-revalidate.C:
			if h.useCase.RequireReadEvent(streamCtx, eventID, claims.UserID) != nil {
				return // removed from the event (or blocked) while the stream was open
			}
		case <-ticker.C:
			next, stampErr := h.useCase.GetSolutionAttemptsStamp(streamCtx, eventID)
			if stampErr != nil {
				return
			}
			if next.Attempts != stamp.Attempts || next.Decisions != stamp.Decisions {
				write(next)
			}
			if next.HintUnlocks != stamp.HintUnlocks {
				writeHints(next)
			}
			stamp = next
		case <-heartbeat.C:
			sse.Heartbeat(ctx.Writer, flusher)
		}
	}
}

// startCSV sends the CSV download headers and the UTF-8 BOM (Excel reads
// Cyrillic correctly only with it).
func startCSV(ctx *gin.Context, name string) *csv.Writer {
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Type", "text/csv; charset=utf-8")
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, name, time.Now().UTC().Format("20060102-1504")))
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Status(http.StatusOK)
	_, _ = ctx.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	return csv.NewWriter(ctx.Writer)
}

// csvText neutralizes spreadsheet formulas in user-controlled text.
func csvText(value string) string { return tools.CSVText(value) }

func csvTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func yesNo(value bool) string {
	if value {
		return "так"
	}
	return "ні"
}
