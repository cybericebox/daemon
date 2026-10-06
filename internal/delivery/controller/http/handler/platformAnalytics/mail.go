package platformAnalytics

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// MailUseCase is the port of the mail section.
type MailUseCase interface {
	GetMail(ctx context.Context, from, to *time.Time, channel, transport, notificationType string, includeTests bool) (platformAnalyticsUseCase.MailView, error)
}

func (h *Handler) initMail(router *gin.RouterGroup) {
	group := router.Group("analytics", h.prot.RequirePermission(rbac.PermAnalyticsRead))
	group.GET("mail", h.mail)
	group.GET("mail/export.csv", h.exportMail)
}

type (
	mailResponse struct {
		Period       periodResponse `json:"Period"`
		Channel      string         `json:"Channel"`
		Transport    string         `json:"Transport"`
		Type         string         `json:"Type"`
		IncludeTests bool           `json:"IncludeTests"`

		Sent   int64 `json:"Sent"`
		Failed int64 `json:"Failed"`
		// Deferred deliveries were held back by the SMTP send limit; not part of Total.
		Deferred int64 `json:"Deferred"`
		Total    int64 `json:"Total"`
		// FailureRate is failed / (sent + failed), 0 with no sends.
		FailureRate float64 `json:"FailureRate"`
		// Fallbacks counts sends that failed on the Event SMTP and went out
		// through the platform transport.
		Fallbacks int64 `json:"Fallbacks"`

		Daily       []mailDayResponse `json:"Daily"`
		ByTransport []mailKeyResponse `json:"ByTransport"`
		// ByChannel: email / in_app tallies; the channel table.
		ByChannel []mailKeyResponse   `json:"ByChannel"`
		ByType    []mailKeyResponse   `json:"ByType"`
		Errors    []mailErrorResponse `json:"Errors"`
		Options   mailOptionsResponse `json:"Options"`
		// Funnels: invitations, registrations and applications of the period; the filters do not apply.
		Funnels dispatchModel.FunnelsSummary `json:"Funnels"`
	}
	mailDayResponse struct {
		Day       time.Time `json:"Day"`
		Sent      int64     `json:"Sent"`
		Failed    int64     `json:"Failed"`
		Fallbacks int64     `json:"Fallbacks"`
	}
	mailKeyResponse struct {
		// Key is a channel (email, in_app), a transport (event, platform, env, unknown) or a notification type.
		Key         string  `json:"Key"`
		Sent        int64   `json:"Sent"`
		Failed      int64   `json:"Failed"`
		Deferred    int64   `json:"Deferred"`
		Fallbacks   int64   `json:"Fallbacks"`
		Total       int64   `json:"Total"`
		FailureRate float64 `json:"FailureRate"`
	}
	mailErrorResponse struct {
		// Code is the enhanced SMTP status, e.g. "550 5.1.1"; may be empty.
		Code string `json:"Code"`
		// Message is the normalized error text; it never holds an address.
		Message string    `json:"Message"`
		Total   int64     `json:"Total"`
		LastAt  time.Time `json:"LastAt"`
	}
	mailOptionsResponse struct {
		Transports []string `json:"Transports"`
		Types      []string `json:"Types"`
	}
)

func toMailResponse(v platformAnalyticsUseCase.MailView) mailResponse {
	out := mailResponse{
		Period:  periodResponse{From: v.Period.From, To: v.Period.To, All: v.Period.All},
		Channel: v.Channel, Transport: v.Transport, Type: v.Type, IncludeTests: v.IncludeTests,
		Sent: v.Sent, Failed: v.Failed, Deferred: v.Deferred, Total: v.Total, FailureRate: v.FailureRate, Fallbacks: v.Fallbacks,
		Daily:       make([]mailDayResponse, 0, len(v.Daily)),
		ByTransport: toMailKeys(v.ByTransport),
		ByChannel:   toMailKeys(v.ByChannel),
		ByType:      toMailKeys(v.ByType),
		Errors:      make([]mailErrorResponse, 0, len(v.Errors)),
		Options:     mailOptionsResponse{Transports: v.Options.Transports, Types: v.Options.Types},
		Funnels:     v.Funnels,
	}
	for _, d := range v.Daily {
		out.Daily = append(out.Daily, mailDayResponse{Day: d.Day, Sent: d.Sent, Failed: d.Failed, Fallbacks: d.Fallbacks})
	}
	for _, e := range v.Errors {
		out.Errors = append(out.Errors, mailErrorResponse{Code: e.Code, Message: e.Message, Total: e.Total, LastAt: e.LastAt})
	}
	return out
}

func toMailKeys(rows []platformAnalyticsUseCase.MailKeyView) []mailKeyResponse {
	out := make([]mailKeyResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, mailKeyResponse{Key: r.Key, Sent: r.Sent, Failed: r.Failed, Deferred: r.Deferred, Fallbacks: r.Fallbacks, Total: r.Total, FailureRate: r.FailureRate})
	}
	return out
}

// parseMailFilters reads channel, transport, type and includeTests.
func parseMailFilters(ctx *gin.Context) (channel, transport, notificationType string, includeTests, ok bool) {
	channel = ctx.Query("channel")
	switch channel {
	case "", platformAnalyticsUseCase.ChannelEmail, platformAnalyticsUseCase.ChannelInApp:
	default:
		response.AbortWithBadRequest(ctx, fmt.Errorf("unknown channel %q", channel))
		return "", "", "", false, false
	}
	if raw := ctx.Query("includeTests"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return "", "", "", false, false
		}
		includeTests = parsed
	}
	return channel, ctx.Query("transport"), ctx.Query("type"), includeTests, true
}

// mail godoc
// @Summary  Platform analytics: notification delivery health
// @Description Email and in-app deliveries per day, per channel, per transport (email: platform / event / env) and per notification type, the failure rate and the top email failure reasons (normalized, never a recipient address). Transport and failure reasons are email-only. SMTP test sends are excluded unless includeTests is set. The response also lists the values the transport and type filters offer.
// @Tags     platform-analytics
// @Produce  json
// @Param    from          query  string  false  "period start (RFC 3339); absent = all time"
// @Param    to            query  string  false  "period end, exclusive (RFC 3339); absent = now"
// @Param    channel       query  string  false  "email | in_app; absent = all"
// @Param    transport     query  string  false  "platform | event | env | unknown (email only)"
// @Param    type          query  string  false  "notification type"
// @Param    includeTests  query  bool    false  "include SMTP test sends (default false)"
// @Success  200   {object}  response.Response{data=mailResponse}
// @Router   /analytics/mail [get]
func (h *Handler) mail(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	channel, transport, kind, includeTests, ok := parseMailFilters(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetMail(ctx.Request.Context(), from, to, channel, transport, kind, includeTests)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toMailResponse(view))
}

// exportMail godoc
// @Summary  Platform analytics: export one mail table as CSV (UTF-8 with BOM)
// @Tags     platform-analytics
// @Produce  text/csv
// @Param    table         query  string  true   "daily | by_channel | by_transport | by_type | errors"
// @Param    from          query  string  false  "period start (RFC 3339)"
// @Param    to            query  string  false  "period end, exclusive (RFC 3339)"
// @Param    channel       query  string  false  "email | in_app; absent = all"
// @Param    transport     query  string  false  "platform | event | env | unknown (email only)"
// @Param    type          query  string  false  "notification type"
// @Param    includeTests  query  bool    false  "include SMTP test sends (default false)"
// @Success  200    {string}  string  "CSV"
// @Router   /analytics/mail/export.csv [get]
func (h *Handler) exportMail(ctx *gin.Context) {
	table := ctx.Query("table")
	switch table {
	case "daily", "by_channel", "by_transport", "by_type", "errors":
	default:
		response.AbortWithError(ctx, platformAnalyticsModel.ErrPlatformAnalyticsTableUnknown.Err())
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	channel, transport, kind, includeTests, ok := parseMailFilters(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetMail(ctx.Request.Context(), from, to, channel, transport, kind, includeTests)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "mail-"+table)
	writeMailCSV(w.Write, table, view)
	w.Flush()
}

// writeMailCSV writes the header and rows of one table.
func writeMailCSV(write func([]string) error, table string, v platformAnalyticsUseCase.MailView) {
	keyRows := func(header string, rows []platformAnalyticsUseCase.MailKeyView) {
		_ = write([]string{header, "Надіслано", "З помилкою", "Усього", "Частка помилок", "Через резервний транспорт"})
		for _, r := range rows {
			_ = write([]string{csvText(r.Key), csvInt(r.Sent), csvInt(r.Failed), csvInt(r.Total), csvFloat(r.FailureRate * 100), csvInt(r.Fallbacks)})
		}
	}
	switch table {
	case "daily":
		_ = write([]string{"День", "Надіслано", "З помилкою", "Через резервний транспорт"})
		for _, d := range v.Daily {
			_ = write([]string{csvDay(d.Day), csvInt(d.Sent), csvInt(d.Failed), csvInt(d.Fallbacks)})
		}
	case "by_channel":
		_ = write([]string{"Канал", "Надіслано", "З помилкою", "Відкладено", "Усього", "Частка помилок"})
		for _, r := range v.ByChannel {
			_ = write([]string{csvText(r.Key), csvInt(r.Sent), csvInt(r.Failed), csvInt(r.Deferred), csvInt(r.Total), csvFloat(r.FailureRate * 100)})
		}
	case "by_transport":
		keyRows("Транспорт", v.ByTransport)
	case "by_type":
		keyRows("Тип сповіщення", v.ByType)
	case "errors":
		_ = write([]string{"Код SMTP", "Помилка", "Кількість", "Востаннє"})
		for _, e := range v.Errors {
			_ = write([]string{csvText(e.Code), csvText(e.Message), csvInt(e.Total), csvTime(e.LastAt)})
		}
	}
}
