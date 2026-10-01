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

// UsageUseCase is the «Використання» part of IUseCase.
type UsageUseCase interface {
	GetEventAnalyticsUsage(ctx context.Context, eventID uuid.UUID, teamID *uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.UsageView, error)
}

// initUsage registers the «Використання» routes on the analytics group.
func (h *Handler) initUsage(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("usage", h.requireSections, h.usage)
	analytics.GET("usage/export.csv", h.requireSections, h.exportUsage)
}

type (
	// usageResponse: per participant, VPN connection state and lab access.
	// Available is false for an event without infrastructure.
	usageResponse struct {
		Available bool                 `json:"Available"`
		At        time.Time            `json:"At"`
		Summary   usageSummaryResponse `json:"Summary"`
		Users     []usageUserResponse  `json:"Users"`
		Period    periodResponse       `json:"Period"`
	}

	usageSummaryResponse struct {
		Users         int64 `json:"Users"`
		OnlineNow     int64 `json:"OnlineNow"`
		VPNUsers      int64 `json:"VPNUsers"`
		ProxyUsers    int64 `json:"ProxyUsers"`
		Sessions      int64 `json:"Sessions"`
		OnlineSeconds int64 `json:"OnlineSeconds"`
		RxBytes       int64 `json:"RxBytes"`
		TxBytes       int64 `json:"TxBytes"`
		ProxyRequests int64 `json:"ProxyRequests"`
		ProxyBytes    int64 `json:"ProxyBytes"`
	}

	usageUserResponse struct {
		UserID   uuid.UUID `json:"UserID"`
		UserName string    `json:"UserName"`
		TeamID   uuid.UUID `json:"TeamID"`
		TeamName string    `json:"TeamName"`
		// LastSeenAt is the last request on the event, LastLabAt the last lab
		// access over the VPN or the proxy; null when never.
		LastSeenAt *time.Time         `json:"LastSeenAt"`
		LastLabAt  *time.Time         `json:"LastLabAt"`
		VPN        usageVPNResponse   `json:"VPN"`
		Proxy      usageProxyResponse `json:"Proxy"`
		Labs       []usageLabResponse `json:"Labs"`
	}

	// usageVPNResponse: Online means the last handshake is recent, never that
	// traffic flowed. Seconds is the online time of the period.
	usageVPNResponse struct {
		Online          bool                   `json:"Online"`
		LastHandshakeAt *time.Time             `json:"LastHandshakeAt"`
		FirstAt         *time.Time             `json:"FirstAt"`
		Sessions        int64                  `json:"Sessions"`
		Seconds         int64                  `json:"Seconds"`
		RxBytes         int64                  `json:"RxBytes"`
		TxBytes         int64                  `json:"TxBytes"`
		Recent          []usageSessionResponse `json:"Recent"`
	}

	usageSessionResponse struct {
		StartedAt time.Time `json:"StartedAt"`
		EndedAt   time.Time `json:"EndedAt"`
		Seconds   int64     `json:"Seconds"`
		RxBytes   int64     `json:"RxBytes"`
		TxBytes   int64     `json:"TxBytes"`
	}

	// usageProxyResponse sums the web proxy use over the whole event.
	usageProxyResponse struct {
		Requests int64      `json:"Requests"`
		BytesIn  int64      `json:"BytesIn"`
		BytesOut int64      `json:"BytesOut"`
		FirstAt  *time.Time `json:"FirstAt"`
		LastAt   *time.Time `json:"LastAt"`
	}

	// usageLabResponse: Surface is vpn or proxy; the counters cover the whole
	// event.
	usageLabResponse struct {
		ChallengeID uuid.UUID `json:"ChallengeID"`
		Task        string    `json:"Task"`
		Surface     string    `json:"Surface"`
		Attempts    int64     `json:"Attempts"`
		BytesIn     int64     `json:"BytesIn"`
		BytesOut    int64     `json:"BytesOut"`
		FirstAt     time.Time `json:"FirstAt"`
		LastAt      time.Time `json:"LastAt"`
	}
)

// usage godoc
// @Summary Per participant VPN connection state and lab access (VPN and web proxy)
// @Description «Використання». Online is true when the last WireGuard handshake is at most 3 minutes old; it is never derived from traffic. VPN figures (sessions, online seconds, bytes) follow the period; the lab counters are cumulative over the whole event. Counts and times only, no addresses. Empty (Available=false) for an event without infrastructure. Not cached.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param team query string false "only this team"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=usageResponse}
// @Router /events/{id}/manage/analytics/usage [get]
func (h *Handler) usage(ctx *gin.Context) {
	v, ok := h.loadUsage(ctx)
	if !ok {
		return
	}
	response.AbortWithData(ctx, toUsageResponse(v))
}

func (h *Handler) loadUsage(ctx *gin.Context) (eventAnalyticsUseCase.UsageView, bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return eventAnalyticsUseCase.UsageView{}, false
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return eventAnalyticsUseCase.UsageView{}, false
	}
	var teamID *uuid.UUID
	if raw := ctx.Query("team"); raw != "" {
		id, err := uuid.FromString(raw)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return eventAnalyticsUseCase.UsageView{}, false
		}
		teamID = &id
	}
	v, err := h.useCase.GetEventAnalyticsUsage(ctx, eventID, teamID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return eventAnalyticsUseCase.UsageView{}, false
	}
	return v, true
}

func toUsageResponse(v eventAnalyticsUseCase.UsageView) usageResponse {
	s := v.Summary
	out := usageResponse{
		Available: v.Available, At: v.At, Period: periodResponse{From: v.Period.From, To: v.Period.To},
		Summary: usageSummaryResponse{
			Users: s.Users, OnlineNow: s.OnlineNow, VPNUsers: s.VPNUsers, ProxyUsers: s.ProxyUsers, Sessions: s.Sessions,
			OnlineSeconds: s.OnlineSeconds, RxBytes: s.RxBytes, TxBytes: s.TxBytes, ProxyRequests: s.ProxyRequests, ProxyBytes: s.ProxyBytes,
		},
		Users: make([]usageUserResponse, 0, len(v.Users)),
	}
	for _, u := range v.Users {
		recent := make([]usageSessionResponse, 0, len(u.VPN.Recent))
		for _, r := range u.VPN.Recent {
			recent = append(recent, usageSessionResponse{StartedAt: r.StartedAt, EndedAt: r.EndedAt, Seconds: r.Seconds, RxBytes: r.RxBytes, TxBytes: r.TxBytes})
		}
		labs := make([]usageLabResponse, 0, len(u.Labs))
		for _, l := range u.Labs {
			labs = append(labs, usageLabResponse{
				ChallengeID: l.ChallengeID, Task: l.Task, Surface: l.Surface, Attempts: l.Attempts,
				BytesIn: l.BytesIn, BytesOut: l.BytesOut, FirstAt: l.FirstAt, LastAt: l.LastAt,
			})
		}
		out.Users = append(out.Users, usageUserResponse{
			UserID: u.UserID, UserName: u.UserName, TeamID: u.TeamID, TeamName: u.TeamName,
			LastSeenAt: u.LastSeenAt, LastLabAt: u.LastLabAt,
			VPN: usageVPNResponse{
				Online: u.VPN.Online, LastHandshakeAt: u.VPN.LastHandshakeAt, FirstAt: u.VPN.FirstAt, Sessions: u.VPN.Sessions,
				Seconds: u.VPN.Seconds, RxBytes: u.VPN.RxBytes, TxBytes: u.VPN.TxBytes, Recent: recent,
			},
			Proxy: usageProxyResponse{
				Requests: u.Proxy.Requests, BytesIn: u.Proxy.BytesIn, BytesOut: u.Proxy.BytesOut, FirstAt: u.Proxy.FirstAt, LastAt: u.Proxy.LastAt,
			},
			Labs: labs,
		})
	}
	return out
}

// exportUsage godoc
// @Summary Per participant usage as CSV (UTF-8 with BOM)
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param team query string false "only this team"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/usage/export.csv [get]
func (h *Handler) exportUsage(ctx *gin.Context) {
	v, ok := h.loadUsage(ctx)
	if !ok {
		return
	}
	w := startAnalyticsCSV(ctx, "usage")
	_ = w.Write(usageCSVHeader)
	for _, u := range v.Users {
		_ = w.Write(usageCSVRow(u))
	}
	w.Flush()
}

var usageCSVHeader = []string{"Учасник", "Команда", "VPN онлайн зараз", "Останнє VPN-підключення (UTC)", "Перше VPN-підключення (UTC)", "VPN сесій", "VPN час онлайн (с)",
	"VPN отримано (байт)", "VPN надіслано (байт)", "Запитів через проксі", "Проксі отримано (байт)", "Проксі надіслано (байт)", "Перший запит через проксі (UTC)", "Останній запит через проксі (UTC)"}

func usageCSVRow(u eventAnalyticsUseCase.UsageUserView) []string {
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	online := "0"
	if u.VPN.Online {
		online = "1"
	}
	return []string{csvCellText(u.UserName), csvCellText(u.TeamName), online, csvTimePtr(u.VPN.LastHandshakeAt), csvTimePtr(u.VPN.FirstAt),
		n(u.VPN.Sessions), n(u.VPN.Seconds), n(u.VPN.RxBytes), n(u.VPN.TxBytes),
		n(u.Proxy.Requests), n(u.Proxy.BytesIn), n(u.Proxy.BytesOut), csvTimePtr(u.Proxy.FirstAt), csvTimePtr(u.Proxy.LastAt)}
}
