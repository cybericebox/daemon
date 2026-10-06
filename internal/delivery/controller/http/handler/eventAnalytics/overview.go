package eventAnalytics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

type (
	overviewResponse struct {
		Participants participantCountsResponse `json:"Participants"`
		Teams        teamCountsResponse        `json:"Teams"`
		Attempts     int64                     `json:"Attempts"`
		// Correct counts effectively correct attempts (after decisions).
		Correct     int64                 `json:"Correct"`
		Solves      int64                 `json:"Solves"`
		HintsOpened int64                 `json:"HintsOpened"`
		HintPoints  int64                 `json:"HintPoints"`
		Stands      standCountsResponse   `json:"Stands"`
		Series      []seriesPointResponse `json:"Series"`
		// Feed: the newest notable moments, newest first.
		Feed    []feedItemResponse `json:"Feed"`
		Markers markersResponse    `json:"Markers"`
		Period  periodResponse     `json:"Period"`
		// RefreshedAt: when the series was last rebuilt (null: not yet).
		RefreshedAt *time.Time `json:"RefreshedAt"`
		// Final: the event's rollup is final (after the finish).
		Final bool `json:"Final"`
		// Leaders: the top of the scoreboard with the gap to the first place;
		// RankedTeams counts the whole board.
		Leaders     []leaderResponse      `json:"Leaders"`
		RankedTeams int64                 `json:"RankedTeams"`
		Tasks       tasksSnapshotResponse `json:"Tasks"`
		Engagement  engagementResponse    `json:"Engagement"`
		// Comms: the mail of the last 24 hours. Null unless the viewer has the
		// sensitive access (§7).
		Comms *commsSnapshotResponse `json:"Comms"`
	}

	leaderResponse struct {
		TeamID uuid.UUID `json:"TeamID"`
		Name   string    `json:"Name"`
		Rank   int64     `json:"Rank"`
		Points int64     `json:"Points"`
		Solved int64     `json:"Solved"`
		// Gap: points behind the first place.
		Gap int64 `json:"Gap"`
	}

	// tasksSnapshotResponse: MostSolved and LeastSolved are among the solved
	// tasks and null when none is solved.
	tasksSnapshotResponse struct {
		Total       int64               `json:"Total"`
		Unsolved    int64               `json:"Unsolved"`
		FirstBloods int64               `json:"FirstBloods"`
		MostSolved  *taskSolvesResponse `json:"MostSolved"`
		LeastSolved *taskSolvesResponse `json:"LeastSolved"`
	}

	taskSolvesResponse struct {
		ChallengeID uuid.UUID `json:"ChallengeID"`
		Name        string    `json:"Name"`
		Solves      int64     `json:"Solves"`
	}

	// engagementResponse: TeamsSolving of Teams admitted teams have a solve.
	engagementResponse struct {
		Teams        int64   `json:"Teams"`
		TeamsSolving int64   `json:"TeamsSolving"`
		AvgSolves    float64 `json:"AvgSolves"`
	}

	commsSnapshotResponse struct {
		EmailSent   int64     `json:"EmailSent"`
		EmailFailed int64     `json:"EmailFailed"`
		Since       time.Time `json:"Since"`
	}

	participantCountsResponse struct {
		Registered int64 `json:"Registered"`
		Approved   int64 `json:"Approved"`
		Pending    int64 `json:"Pending"`
		Invited    int64 `json:"Invited"`
		Active     int64 `json:"Active"`
	}

	teamCountsResponse struct {
		Total      int64 `json:"Total"`
		Admitted   int64 `json:"Admitted"`
		Incomplete int64 `json:"Incomplete"`
	}

	standCountsResponse struct {
		Creating int64 `json:"Creating"`
		Ready    int64 `json:"Ready"`
		Failed   int64 `json:"Failed"`
	}

	// seriesPointResponse is one 5-minute bucket starting at At.
	seriesPointResponse struct {
		At       time.Time `json:"At"`
		Attempts int64     `json:"Attempts"`
		Correct  int64     `json:"Correct"`
		Solves   int64     `json:"Solves"`
		Opens    int64     `json:"Opens"`
	}

	// feedItemResponse: Kind is first_blood, stand_failed, team_created or
	// freeze_started.
	feedItemResponse struct {
		Kind          string     `json:"Kind"`
		At            time.Time  `json:"At"`
		TeamID        *uuid.UUID `json:"TeamID"`
		TeamName      string     `json:"TeamName"`
		ChallengeName string     `json:"ChallengeName"`
		Detail        string     `json:"Detail"`
	}

	markersResponse struct {
		StartAt  time.Time  `json:"StartAt"`
		FreezeAt *time.Time `json:"FreezeAt"`
		FinishAt *time.Time `json:"FinishAt"`
	}

	periodResponse struct {
		From time.Time `json:"From"`
		To   time.Time `json:"To"`
	}
)

// overview godoc
// @Summary Event analytics overview (counters and the 5-minute activity series)
// @Description §6.1 «Огляд». Without from/to the period is the event's own window (start to finish, or to now while it runs). The series has a point per 5 minutes; values are cached for a few seconds.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=overviewResponse}
// @Router /events/{id}/manage/analytics/overview [get]
func (h *Handler) overview(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsOverview(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := toOverviewResponse(v)
	// The mail counters are for the sensitive access only (§7).
	if !h.sensitiveViewer(ctx, eventID) {
		out.Comms = nil
	}
	response.AbortWithData(ctx, out)
}

// sensitiveViewer reports whether the caller has the sensitive access. The
// route already admitted them to the sections, so a failed lookup only hides
// the sensitive parts.
func (h *Handler) sensitiveViewer(ctx *gin.Context, eventID uuid.UUID) bool {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		return false
	}
	access, err := h.useCase.EventAnalyticsAccess(ctx, eventID, claims)
	return err == nil && access.Sensitive
}

func toOverviewResponse(v eventAnalyticsUseCase.OverviewView) overviewResponse {
	leaders := make([]leaderResponse, 0, len(v.Leaders))
	for _, l := range v.Leaders {
		leaders = append(leaders, leaderResponse{TeamID: l.TeamID, Name: l.Name, Rank: l.Rank, Points: l.Points, Solved: l.Solved, Gap: l.Gap})
	}
	solves := func(t *eventAnalyticsUseCase.TaskSolvesView) *taskSolvesResponse {
		if t == nil {
			return nil
		}
		return &taskSolvesResponse{ChallengeID: t.ChallengeID, Name: t.Name, Solves: t.Solves}
	}
	series := make([]seriesPointResponse, 0, len(v.Series))
	for _, p := range v.Series {
		series = append(series, seriesPointResponse{At: p.At, Attempts: p.Attempts, Correct: p.Correct, Solves: p.Solves, Opens: p.Opens})
	}
	feed := make([]feedItemResponse, 0, len(v.Feed))
	for _, f := range v.Feed {
		feed = append(feed, feedItemResponse{Kind: f.Kind, At: f.At, TeamID: f.TeamID, TeamName: f.TeamName, ChallengeName: f.ChallengeName, Detail: f.Detail})
	}
	return overviewResponse{
		Participants: participantCountsResponse{
			Registered: v.Participants.Registered, Approved: v.Participants.Approved, Pending: v.Participants.Pending,
			Invited: v.Participants.Invited, Active: v.Participants.Active,
		},
		Teams:       teamCountsResponse{Total: v.Teams.Total, Admitted: v.Teams.Admitted, Incomplete: v.Teams.Incomplete},
		Attempts:    v.Attempts,
		Correct:     v.Correct,
		Solves:      v.Solves,
		HintsOpened: v.HintsOpened,
		HintPoints:  v.HintPoints,
		Stands:      standCountsResponse{Creating: v.Stands.Creating, Ready: v.Stands.Ready, Failed: v.Stands.Failed},
		Series:      series,
		Feed:        feed,
		Markers:     markersResponse{StartAt: v.Markers.StartAt, FreezeAt: v.Markers.FreezeAt, FinishAt: v.Markers.FinishAt},
		Period:      periodResponse{From: v.Period.From, To: v.Period.To},
		RefreshedAt: v.RefreshedAt,
		Final:       v.Final,
		Leaders:     leaders,
		RankedTeams: v.RankedTeams,
		Tasks: tasksSnapshotResponse{
			Total: v.Tasks.Total, Unsolved: v.Tasks.Unsolved, FirstBloods: v.Tasks.FirstBloods,
			MostSolved: solves(v.Tasks.MostSolved), LeastSolved: solves(v.Tasks.LeastSolved),
		},
		Engagement: engagementResponse{Teams: v.Engagement.Teams, TeamsSolving: v.Engagement.TeamsSolving, AvgSolves: v.Engagement.AvgSolves},
		Comms:      &commsSnapshotResponse{EmailSent: v.Comms.EmailSent, EmailFailed: v.Comms.EmailFailed, Since: v.Comms.Since},
	}
}

// exportOverview godoc
// @Summary Export the overview's 5-minute activity series as CSV (UTF-8 with BOM)
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/overview/export.csv [get]
func (h *Handler) exportOverview(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsOverview(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startAnalyticsCSV(ctx, "overview")
	_ = w.Write([]string{"Початок інтервалу (UTC)", "Спроби", "Правильні спроби", "Розвʼязання", "Відкриття завдань"})
	for _, p := range v.Series {
		_ = w.Write([]string{p.At.UTC().Format(time.RFC3339), strconv.FormatInt(p.Attempts, 10), strconv.FormatInt(p.Correct, 10),
			strconv.FormatInt(p.Solves, 10), strconv.FormatInt(p.Opens, 10)})
	}
	w.Flush()
}
