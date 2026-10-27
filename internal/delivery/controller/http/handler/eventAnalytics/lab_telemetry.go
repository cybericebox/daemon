package eventAnalytics

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// LabTelemetryUseCase is the lab telemetry part of IUseCase.
type LabTelemetryUseCase interface {
	GetEventLabTelemetry(ctx context.Context, eventID, teamID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.LabTelemetryView, error)
	GetEventLabTransitions(ctx context.Context, eventID, teamID uuid.UUID, from, to *time.Time, kinds []string) (eventAnalyticsUseCase.LabTransitionsView, error)
}

// initLabTelemetry registers the lab telemetry routes on the analytics group.
func (h *Handler) initLabTelemetry(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("lab-telemetry", h.requireSections, h.labTelemetry)
	analytics.GET("lab-telemetry/transitions", h.requireSections, h.labTransitions)
}

type (
	// labTelemetryResponse: the minute rollups of a team's labs. Averages are
	// null without samples. Truncated is true when a list hit its row limit.
	labTelemetryResponse struct {
		Available bool                   `json:"Available"`
		Period    periodResponse         `json:"Period"`
		Devices   []deviceMinuteResponse `json:"Devices"`
		Labs      []labMinuteResponse    `json:"Labs"`
		Clients   []clientMinuteResponse `json:"Clients"`
		Truncated bool                   `json:"Truncated"`
	}

	// deviceMinuteResponse: CPU in millicores, memory in bytes. Restarts is
	// the restart counter seen in the minute; LastPodPhase the last pod phase.
	deviceMinuteResponse struct {
		LabGroupName string    `json:"LabGroupName"`
		LabName      string    `json:"LabName"`
		DeviceName   string    `json:"DeviceName"`
		At           time.Time `json:"At"`
		Samples      int32     `json:"Samples"`
		ReadySamples int32     `json:"ReadySamples"`
		CPUAvg       *int64    `json:"CPUAvgMillicores"`
		CPUMax       *int64    `json:"CPUMaxMillicores"`
		MemoryAvg    *int64    `json:"MemoryAvgBytes"`
		MemoryMax    *int64    `json:"MemoryMaxBytes"`
		Restarts     int64     `json:"Restarts"`
		LastPodPhase string    `json:"LastPodPhase"`
	}

	labMinuteResponse struct {
		LabGroupName    string    `json:"LabGroupName"`
		LabName         string    `json:"LabName"`
		At              time.Time `json:"At"`
		Samples         int32     `json:"Samples"`
		ReadySamples    int32     `json:"ReadySamples"`
		VPNReadySamples int32     `json:"VPNReadySamples"`
	}

	// clientMinuteResponse: the traffic of the participant's VPN client in the
	// minute and how many samples saw it connected.
	clientMinuteResponse struct {
		LabGroupName     string    `json:"LabGroupName"`
		ClientName       string    `json:"ClientName"`
		At               time.Time `json:"At"`
		Samples          int32     `json:"Samples"`
		ConnectedSamples int32     `json:"ConnectedSamples"`
		RxBytes          int64     `json:"RxBytes"`
		TxBytes          int64     `json:"TxBytes"`
	}

	labTransitionsResponse struct {
		Available   bool                    `json:"Available"`
		Period      periodResponse          `json:"Period"`
		Transitions []labTransitionResponse `json:"Transitions"`
		Truncated   bool                    `json:"Truncated"`
	}

	// labTransitionResponse: At is the agent's time, ReceivedAt the platform's
	// time of the observation (ReportLagMillis between them). For lab_ready
	// PlatformReadyAt is when the platform marked the lab ready and
	// PlatformLagMillis its delay after what the agent saw; both null when the
	// platform has no such change.
	labTransitionResponse struct {
		ID                uuid.UUID  `json:"ID"`
		Kind              string     `json:"Kind"`
		LabGroupName      string     `json:"LabGroupName"`
		LabName           string     `json:"LabName"`
		DeviceName        *string    `json:"DeviceName"`
		ClientName        *string    `json:"ClientName"`
		At                time.Time  `json:"At"`
		ReceivedAt        time.Time  `json:"ReceivedAt"`
		ReportLagMillis   int64      `json:"ReportLagMillis"`
		Detail            *string    `json:"Detail"`
		PlatformReadyAt   *time.Time `json:"PlatformReadyAt"`
		PlatformLagMillis *int64     `json:"PlatformLagMillis"`
	}
)

// labTelemetry godoc
// @Summary Minute rollups of a team's labs: devices, labs, VPN clients
// @Description Per minute: device CPU and memory averages and peaks, ready samples, restarts and last pod phase; lab ready and VPN ready samples; each VPN client's traffic and connected samples. Empty (Available=false) for an event without infrastructure. The period defaults to the event.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param team_id query string true "team ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=labTelemetryResponse}
// @Router /events/{id}/manage/analytics/lab-telemetry [get]
func (h *Handler) labTelemetry(ctx *gin.Context) {
	eventID, teamID, from, to, ok := parseLabTelemetryQuery(ctx)
	if !ok {
		return
	}
	people, ok := h.seesPeople(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventLabTelemetry(ctx, eventID, teamID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if !people {
		// The VPN clients are participants: a viewer without the people level sees containers and labs only.
		v.Clients = nil
	}
	out := labTelemetryResponse{
		Available: v.Available, Period: periodResponse{From: v.Period.From, To: v.Period.To}, Truncated: v.Truncated,
		Devices: make([]deviceMinuteResponse, 0, len(v.Devices)),
		Labs:    make([]labMinuteResponse, 0, len(v.Labs)),
		Clients: make([]clientMinuteResponse, 0, len(v.Clients)),
	}
	for _, d := range v.Devices {
		r := deviceMinuteResponse{
			LabGroupName: d.LabGroupName, LabName: d.LabName, DeviceName: d.DeviceName, At: d.BucketAt,
			Samples: d.Samples, ReadySamples: d.ReadySamples, CPUMax: d.CPUMax, MemoryMax: d.MemoryMax,
			Restarts: d.Restarts, LastPodPhase: d.LastPodPhase,
		}
		if d.CPUSamples > 0 {
			avg := d.CPUSum / int64(d.CPUSamples)
			r.CPUAvg = &avg
		}
		if d.MemorySamples > 0 {
			avg := d.MemorySum / int64(d.MemorySamples)
			r.MemoryAvg = &avg
		}
		out.Devices = append(out.Devices, r)
	}
	for _, l := range v.Labs {
		out.Labs = append(out.Labs, labMinuteResponse{
			LabGroupName: l.LabGroupName, LabName: l.LabName, At: l.BucketAt, Samples: l.Samples,
			ReadySamples: l.ReadySamples, VPNReadySamples: l.VPNReadySamples,
		})
	}
	for _, c := range v.Clients {
		out.Clients = append(out.Clients, clientMinuteResponse{
			LabGroupName: c.LabGroupName, ClientName: c.ClientName, At: c.BucketAt, Samples: c.Samples,
			ConnectedSamples: c.ConnectedSamples, RxBytes: c.RxBytes, TxBytes: c.TxBytes,
		})
	}
	response.AbortWithData(ctx, out)
}

// labTransitions godoc
// @Summary Transitions journal of a team's labs
// @Description State changes seen in the lab telemetry, oldest first: lab_ready, lab_not_ready, device_ready, device_restart, pod_scheduled, pod_started, vpn_boot_changed, policy_applied, client_connected, client_disconnected. For lab_ready also when the platform marked the lab ready. Empty (Available=false) for an event without infrastructure.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param team_id query string true "team ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Param kinds query string false "comma-separated kinds; all when empty"
// @Success 200 {object} response.Response{data=labTransitionsResponse}
// @Router /events/{id}/manage/analytics/lab-telemetry/transitions [get]
func (h *Handler) labTransitions(ctx *gin.Context) {
	eventID, teamID, from, to, ok := parseLabTelemetryQuery(ctx)
	if !ok {
		return
	}
	people, ok := h.seesPeople(ctx)
	if !ok {
		return
	}
	var kinds []string
	if raw := ctx.Query("kinds"); raw != "" {
		for _, kind := range strings.Split(raw, ",") {
			kind = strings.TrimSpace(kind)
			if !slices.Contains(eventAnalyticsUseCase.LabTransitionKinds, kind) {
				response.AbortWithBadRequest(ctx, errors.New("unknown transition kind: "+kind))
				return
			}
			kinds = append(kinds, kind)
		}
	}
	if !people {
		// Client connects and disconnects name a participant: left out without the people level.
		if kinds = withoutClientKinds(kinds); len(kinds) == 0 {
			response.AbortWithData(ctx, labTransitionsResponse{
				Available: true, Transitions: []labTransitionResponse{},
			})
			return
		}
	}
	v, err := h.useCase.GetEventLabTransitions(ctx, eventID, teamID, from, to, kinds)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := labTransitionsResponse{
		Available: v.Available, Period: periodResponse{From: v.Period.From, To: v.Period.To}, Truncated: v.Truncated,
		Transitions: make([]labTransitionResponse, 0, len(v.Transitions)),
	}
	for _, t := range v.Transitions {
		r := labTransitionResponse{
			ID: t.ID, Kind: t.Kind, LabGroupName: t.LabGroupName, LabName: t.LabName, DeviceName: t.DeviceName,
			ClientName: t.ClientName, At: t.At, ReceivedAt: t.ReceivedAt,
			ReportLagMillis: t.ReceivedAt.Sub(t.At).Milliseconds(), Detail: t.Detail, PlatformReadyAt: t.PlatformReadyAt,
		}
		if t.PlatformReadyAt != nil {
			lag := t.PlatformReadyAt.Sub(t.At).Milliseconds()
			r.PlatformLagMillis = &lag
		}
		out.Transitions = append(out.Transitions, r)
	}
	response.AbortWithData(ctx, out)
}

// withoutClientKinds narrows a kinds filter to the kinds that name no participant; an empty filter
// (all kinds) becomes every such kind. An empty result means nothing is left to show.
func withoutClientKinds(kinds []string) []string {
	if len(kinds) == 0 {
		kinds = eventAnalyticsUseCase.LabTransitionKinds
	}
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if !strings.HasPrefix(kind, "client_") {
			out = append(out, kind)
		}
	}
	return out
}

// parseLabTelemetryQuery reads the event, the required team and the optional period.
func parseLabTelemetryQuery(ctx *gin.Context) (eventID, teamID uuid.UUID, from, to *time.Time, ok bool) {
	if eventID, ok = parseEventID(ctx); !ok {
		return
	}
	teamID, err := uuid.FromString(ctx.Query("team_id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, nil, nil, false
	}
	if from, to, ok = parsePeriod(ctx); !ok {
		return
	}
	return eventID, teamID, from, to, true
}
