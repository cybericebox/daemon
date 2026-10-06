package platformAnalytics

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// OverviewUseCase is the port of the overview section.
type OverviewUseCase interface {
	GetOverview(ctx context.Context, from, to *time.Time) (platformAnalyticsUseCase.OverviewView, error)
}

type (
	overviewResponse struct {
		Period       overviewPeriodResponse       `json:"Period"`
		Users        overviewUsersResponse        `json:"Users"`
		Events       overviewEventsResponse       `json:"Events"`
		Participants overviewParticipantsResponse `json:"Participants"`
		Activity     overviewActivityResponse     `json:"Activity"`
		Mail         overviewMailResponse         `json:"Mail"`
		Stands       overviewStandsResponse       `json:"Stands"`
		Series       overviewSeriesResponse       `json:"Series"`
	}

	overviewPeriodResponse struct {
		periodResponse
		// Previous is the window of the same length before this one; null for all time.
		Previous *overviewBoundsResponse `json:"Previous"`
	}

	overviewBoundsResponse struct {
		From time.Time `json:"From"`
		To   time.Time `json:"To"`
	}

	// overviewMetricResponse is a value of the period and the previous
	// period's one (null for all time).
	overviewMetricResponse struct {
		Value    int64  `json:"Value"`
		Previous *int64 `json:"Previous"`
	}

	overviewUsersResponse struct {
		Total  int64                  `json:"Total"`
		New    overviewMetricResponse `json:"New"`
		Active overviewMetricResponse `json:"Active"`
	}

	overviewEventsResponse struct {
		Draft     int64                  `json:"Draft"`
		Published int64                  `json:"Published"`
		Running   int64                  `json:"Running"`
		Finished  int64                  `json:"Finished"`
		Archived  int64                  `json:"Archived"`
		Total     int64                  `json:"Total"`
		New       overviewMetricResponse `json:"New"`
	}

	overviewParticipantsResponse struct {
		Registered overviewMetricResponse `json:"Registered"`
		Approved   overviewMetricResponse `json:"Approved"`
	}

	overviewActivityResponse struct {
		Attempts overviewMetricResponse `json:"Attempts"`
		Solves   overviewMetricResponse `json:"Solves"`
	}

	overviewMailResponse struct {
		Sent   overviewMetricResponse `json:"Sent"`
		Failed overviewMetricResponse `json:"Failed"`
	}

	overviewStandsResponse struct {
		Ready    int64                  `json:"Ready"`
		Creating int64                  `json:"Creating"`
		Failed   int64                  `json:"Failed"`
		Failures overviewMetricResponse `json:"Failures"`
	}

	overviewSeriesResponse struct {
		NewUsers []overviewDayNewResponse      `json:"NewUsers"`
		Activity []overviewDayActivityResponse `json:"Activity"`
		Mail     []overviewDayMailResponse     `json:"Mail"`
	}

	overviewDayNewResponse struct {
		Day time.Time `json:"Day"`
		New int64     `json:"New"`
	}

	overviewDayActivityResponse struct {
		Day      time.Time `json:"Day"`
		Attempts int64     `json:"Attempts"`
		Solves   int64     `json:"Solves"`
	}

	overviewDayMailResponse struct {
		Day    time.Time `json:"Day"`
		Sent   int64     `json:"Sent"`
		Failed int64     `json:"Failed"`
	}
)

func (h *Handler) initOverview(router *gin.RouterGroup) {
	group := router.Group("analytics", h.prot.RequirePermission(rbac.PermAnalyticsRead))
	group.GET("overview", h.overview)
	group.GET("overview/export.csv", h.exportOverview)
}

func toOverviewPeriodResponse(p platformAnalyticsUseCase.OverviewPeriodView) overviewPeriodResponse {
	out := overviewPeriodResponse{periodResponse: periodResponse{From: p.From, To: p.To, All: p.All}}
	if p.Previous != nil {
		out.Previous = &overviewBoundsResponse{From: p.Previous.From, To: p.Previous.To}
	}
	return out
}

func toOverviewMetricResponse(m platformAnalyticsUseCase.OverviewMetricView) overviewMetricResponse {
	return overviewMetricResponse{Value: m.Value, Previous: m.Previous}
}

func toOverviewResponse(v platformAnalyticsUseCase.OverviewView) overviewResponse {
	out := overviewResponse{
		Period: toOverviewPeriodResponse(v.Period),
		Users:  overviewUsersResponse{Total: v.Users.Total, New: toOverviewMetricResponse(v.Users.New), Active: toOverviewMetricResponse(v.Users.Active)},
		Events: overviewEventsResponse{
			Draft: v.Events.Draft, Published: v.Events.Published, Running: v.Events.Running, Finished: v.Events.Finished,
			Archived: v.Events.Archived, Total: v.Events.Total, New: toOverviewMetricResponse(v.Events.New),
		},
		Participants: overviewParticipantsResponse{
			Registered: toOverviewMetricResponse(v.Participants.Registered), Approved: toOverviewMetricResponse(v.Participants.Approved),
		},
		Activity: overviewActivityResponse{Attempts: toOverviewMetricResponse(v.Activity.Attempts), Solves: toOverviewMetricResponse(v.Activity.Solves)},
		Mail:     overviewMailResponse{Sent: toOverviewMetricResponse(v.Mail.Sent), Failed: toOverviewMetricResponse(v.Mail.Failed)},
		Stands: overviewStandsResponse{
			Ready: v.Stands.Ready, Creating: v.Stands.Creating, Failed: v.Stands.Failed, Failures: toOverviewMetricResponse(v.Stands.Failures),
		},
		Series: overviewSeriesResponse{
			NewUsers: make([]overviewDayNewResponse, 0, len(v.Series.NewUsers)),
			Activity: make([]overviewDayActivityResponse, 0, len(v.Series.Activity)),
			Mail:     make([]overviewDayMailResponse, 0, len(v.Series.Mail)),
		},
	}
	for _, d := range v.Series.NewUsers {
		out.Series.NewUsers = append(out.Series.NewUsers, overviewDayNewResponse{Day: d.Day, New: d.New})
	}
	for _, d := range v.Series.Activity {
		out.Series.Activity = append(out.Series.Activity, overviewDayActivityResponse{Day: d.Day, Attempts: d.Attempts, Solves: d.Solves})
	}
	for _, d := range v.Series.Mail {
		out.Series.Mail = append(out.Series.Mail, overviewDayMailResponse{Day: d.Day, Sent: d.Sent, Failed: d.Failed})
	}
	return out
}

// overview godoc
// @Summary Platform overview analytics
// @Description Headline numbers of the platform for the period (users, events by status, participants, attempts and solves, emails, stands), each with the value of the previous period of the same length (null for all time), and daily series of new users, attempts and solves, and emails. from/to are RFC 3339; no from means all time.
// @Tags platform-analytics
// @Produce json
// @Param from query string false "period start (RFC 3339); absent = all time"
// @Param to query string false "period end, exclusive (RFC 3339); absent = now"
// @Success 200 {object} response.Response{data=overviewResponse}
// @Router /analytics/overview [get]
func (h *Handler) overview(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetOverview(ctx, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toOverviewResponse(v))
}

// exportOverview godoc
// @Summary Export one table of the overview analytics as CSV (UTF-8 with BOM)
// @Description table is summary (every headline number with the previous period) or series (the daily series).
// @Tags platform-analytics
// @Produce text/csv
// @Param table query string true "summary | series"
// @Param from query string false "period start (RFC 3339); absent = all time"
// @Param to query string false "period end, exclusive (RFC 3339); absent = now"
// @Success 200 {string} string "CSV"
// @Router /analytics/overview/export.csv [get]
func (h *Handler) exportOverview(ctx *gin.Context) {
	table := ctx.Query("table")
	if table != "summary" && table != "series" {
		response.AbortWithError(ctx, platformAnalyticsModel.ErrPlatformAnalyticsTableUnknown.Err())
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetOverview(ctx, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "overview-"+table)
	if table == "summary" {
		writeOverviewSummary(w, v)
	} else {
		writeOverviewSeries(w, v)
	}
	w.Flush()
}
