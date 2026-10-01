package eventAnalytics

import (
	"context"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// StandsUseCase is the «Стенди» part of IUseCase.
type StandsUseCase interface {
	GetEventAnalyticsStands(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.StandsView, error)
}

// initStands registers the «Стенди» routes on the analytics group.
func (h *Handler) initStands(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("stands", h.requireSections, h.stands)
	analytics.GET("stands/export.csv", h.requireSections, h.exportStands)
}

type (
	// standsResponse: durations are seconds and null when there is nothing
	// to measure. Available is false for an event without infrastructure.
	standsResponse struct {
		Available bool                  `json:"Available"`
		Summary   standsSummaryResponse `json:"Summary"`
		Teams     []standTeamResponse   `json:"Teams"`
		Period    periodResponse        `json:"Period"`
	}

	standsSummaryResponse struct {
		Teams        int64  `json:"Teams"`
		Ready        int64  `json:"Ready"`
		Creating     int64  `json:"Creating"`
		Failed       int64  `json:"Failed"`
		NotDeployed  int64  `json:"NotDeployed"`
		DeployAvg    *int64 `json:"DeployAvgSeconds"`
		DeployMedian *int64 `json:"DeployMedianSeconds"`
		DeployMax    *int64 `json:"DeployMaxSeconds"`
		Failures     int64  `json:"Failures"`
		Unresolved   int64  `json:"Unresolved"`
		RecoveryAvg  *int64 `json:"RecoveryAvgSeconds"`
		RecoveryMax  *int64 `json:"RecoveryMaxSeconds"`
		Restarts     int64  `json:"Restarts"`
		VPNTeams     int64  `json:"VPNTeams"`
		VPNSessions  int64  `json:"VPNSessions"`
		VPNRxBytes   int64  `json:"VPNRxBytes"`
		VPNTxBytes   int64  `json:"VPNTxBytes"`
	}

	// standTeamResponse: Status is not_deployed, creating, ready, failed or
	// removed.
	standTeamResponse struct {
		TeamID          uuid.UUID              `json:"TeamID"`
		TeamName        string                 `json:"TeamName"`
		Status          string                 `json:"Status"`
		Reason          string                 `json:"Reason"`
		StatusChangedAt *time.Time             `json:"StatusChangedAt"`
		DeploySeconds   *int64                 `json:"DeploySeconds"`
		Generations     int                    `json:"Generations"`
		FailureCount    int                    `json:"FailureCount"`
		Unresolved      int                    `json:"Unresolved"`
		RecoveryAvg     *int64                 `json:"RecoveryAvgSeconds"`
		RecoveryMax     *int64                 `json:"RecoveryMaxSeconds"`
		Failures        []standFailureResponse `json:"Failures"`
		Resources       standResourcesResponse `json:"Resources"`
		VPN             standVPNResponse       `json:"VPN"`
	}

	// standFailureResponse: Source is stand or lab; Task names the task of a
	// lab failure.
	standFailureResponse struct {
		Source          string     `json:"Source"`
		Task            string     `json:"Task"`
		At              time.Time  `json:"At"`
		Reason          string     `json:"Reason"`
		RecoveredAt     *time.Time `json:"RecoveredAt"`
		RecoverySeconds *int64     `json:"RecoverySeconds"`
	}

	// standResourcesResponse: the sum of the team's devices' peaks over the
	// period (CPU in millicores, memory in bytes).
	standResourcesResponse struct {
		Devices          int64 `json:"Devices"`
		PeakCPUMillis    int64 `json:"PeakCPUMillicores"`
		PeakMemoryBytes  int64 `json:"PeakMemoryBytes"`
		Restarts         int64 `json:"Restarts"`
		RestartedDevices int64 `json:"RestartedDevices"`
	}

	standVPNResponse struct {
		Sessions int64      `json:"Sessions"`
		Users    int64      `json:"Users"`
		Members  int64      `json:"Members"`
		Seconds  int64      `json:"Seconds"`
		RxBytes  int64      `json:"RxBytes"`
		TxBytes  int64      `json:"TxBytes"`
		LastAt   *time.Time `json:"LastAt"`
	}
)

// stands godoc
// @Summary Team stands: deploy time, status, failures, telemetry, VPN
// @Description §6.5 «Стенди». Empty (Available=false) for an event without infrastructure. The period bounds the telemetry and VPN figures; deploy and failure history is the event's.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=standsResponse}
// @Router /events/{id}/manage/analytics/stands [get]
func (h *Handler) stands(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsStands(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStandsResponse(v))
}

func toStandsResponse(v eventAnalyticsUseCase.StandsView) standsResponse {
	s := v.Summary
	out := standsResponse{
		Available: v.Available, Period: periodResponse{From: v.Period.From, To: v.Period.To},
		Summary: standsSummaryResponse{
			Teams: s.Teams, Ready: s.Ready, Creating: s.Creating, Failed: s.Failed, NotDeployed: s.NotDeployed,
			DeployAvg: s.DeployAvg, DeployMedian: s.DeployMedian, DeployMax: s.DeployMax,
			Failures: s.Failures, Unresolved: s.Unresolved, RecoveryAvg: s.RecoveryAvg, RecoveryMax: s.RecoveryMax,
			Restarts: s.Restarts, VPNTeams: s.VPNTeams, VPNSessions: s.VPNSessions, VPNRxBytes: s.VPNRxBytes, VPNTxBytes: s.VPNTxBytes,
		},
		Teams: make([]standTeamResponse, 0, len(v.Teams)),
	}
	for _, t := range v.Teams {
		failures := make([]standFailureResponse, 0, len(t.Failures))
		for _, f := range t.Failures {
			failures = append(failures, standFailureResponse{
				Source: f.Source, Task: f.Task, At: f.At, Reason: f.Reason, RecoveredAt: f.RecoveredAt, RecoverySeconds: f.RecoverySeconds,
			})
		}
		out.Teams = append(out.Teams, standTeamResponse{
			TeamID: t.TeamID, TeamName: t.TeamName, Status: t.Status, Reason: t.Reason, StatusChangedAt: t.StatusChangedAt,
			DeploySeconds: t.DeploySeconds, Generations: t.Generations, FailureCount: t.FailureCount, Unresolved: t.Unresolved,
			RecoveryAvg: t.RecoveryAvg, RecoveryMax: t.RecoveryMax, Failures: failures,
			Resources: standResourcesResponse{
				Devices: t.Resources.Devices, PeakCPUMillis: t.Resources.PeakCPUMillis, PeakMemoryBytes: t.Resources.PeakMemoryBytes,
				Restarts: t.Resources.Restarts, RestartedDevices: t.Resources.RestartedDevices,
			},
			VPN: standVPNResponse{
				Sessions: t.VPN.Sessions, Users: t.VPN.Users, Members: t.VPN.Members, Seconds: t.VPN.Seconds,
				RxBytes: t.VPN.RxBytes, TxBytes: t.VPN.TxBytes, LastAt: t.VPN.LastAt,
			},
		})
	}
	return out
}

// exportStands godoc
// @Summary Team stands as CSV (UTF-8 with BOM)
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/stands/export.csv [get]
func (h *Handler) exportStands(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsStands(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startAnalyticsCSV(ctx, "stands")
	_ = w.Write(standsCSVHeader)
	for _, t := range v.Teams {
		_ = w.Write(standsCSVRow(t))
	}
	w.Flush()
}

var standsCSVHeader = []string{"Команда", "Статус", "Причина", "Розгортання (с)", "Розгортань", "Збоїв", "Не усунено", "Середнє відновлення (с)", "Найдовше відновлення (с)",
	"Пристроїв", "Пік CPU (міліядер)", "Пік памʼяті (байт)", "Перезапусків", "VPN сесій", "VPN учасників", "Учасників у команді", "VPN секунд", "VPN отримано (байт)", "VPN надіслано (байт)", "Остання VPN активність (UTC)"}

func standsCSVRow(t eventAnalyticsUseCase.StandTeamView) []string {
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	return []string{csvCellText(t.TeamName), t.Status, csvCellText(t.Reason), csvSeconds(t.DeploySeconds), strconv.Itoa(t.Generations),
		strconv.Itoa(t.FailureCount), strconv.Itoa(t.Unresolved), csvSeconds(t.RecoveryAvg), csvSeconds(t.RecoveryMax),
		n(t.Resources.Devices), n(t.Resources.PeakCPUMillis), n(t.Resources.PeakMemoryBytes), n(t.Resources.Restarts),
		n(t.VPN.Sessions), n(t.VPN.Users), n(t.VPN.Members), n(t.VPN.Seconds), n(t.VPN.RxBytes), n(t.VPN.TxBytes), csvTimePtr(t.VPN.LastAt)}
}
