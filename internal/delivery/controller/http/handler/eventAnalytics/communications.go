package eventAnalytics

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

type (
	communicationsResponse struct {
		// Totals sums every type (its Type is empty).
		Totals commsTypeResponse        `json:"Totals"`
		Types  []commsTypeResponse      `json:"Types"`
		Forms  []formCompletionResponse `json:"Forms"`
		// Funnels: invitations, registrations and applications of the event.
		Funnels dispatchModel.FunnelsSummary `json:"Funnels"`
		Period  optionalPeriodResponse       `json:"Period"`
	}

	// commsTypeResponse: Sent is handed to the transport, Errors failed.
	commsTypeResponse struct {
		Type         string `json:"Type"`
		EmailSent    int64  `json:"EmailSent"`
		EmailErrors  int64  `json:"EmailErrors"`
		InAppSent    int64  `json:"InAppSent"`
		InAppErrors  int64  `json:"InAppErrors"`
		InAppCreated int64  `json:"InAppCreated"`
		InAppRead    int64  `json:"InAppRead"`
		// ReadRate is InAppRead / InAppCreated (0..1), null without in-app items.
		ReadRate *float64 `json:"ReadRate"`
	}

	formCompletionResponse struct {
		ID           uuid.UUID `json:"ID"`
		Title        string    `json:"Title"`
		Registration bool      `json:"Registration"`
		Enabled      bool      `json:"Enabled"`
		Assigned     int64     `json:"Assigned"`
		Completed    int64     `json:"Completed"`
		Answers      int64     `json:"Answers"`
		// CompletionRate is Completed / Assigned (0..1), null when nothing was assigned.
		CompletionRate *float64 `json:"CompletionRate"`
	}
)

// communications godoc
// @Summary Communications analytics
// @Description §6.7 «Комунікації»: emails and in-app notifications sent per type, errors, the in-app read rate, and the completion of every event form. from/to limit the window (open bounds by default).
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=communicationsResponse}
// @Router /events/{id}/manage/analytics/communications [get]
func (h *Handler) communications(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsCommunications(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toCommunicationsResponse(v))
}

func toCommsType(t eventAnalyticsUseCase.CommsTypeView) commsTypeResponse {
	return commsTypeResponse{
		Type: t.Type, EmailSent: t.EmailSent, EmailErrors: t.EmailErrors, InAppSent: t.InAppSent, InAppErrors: t.InAppErrors,
		InAppCreated: t.InAppCreated, InAppRead: t.InAppRead, ReadRate: t.ReadRate,
	}
}

func toCommunicationsResponse(v eventAnalyticsUseCase.CommunicationsView) communicationsResponse {
	out := communicationsResponse{
		Totals:  toCommsType(v.Totals),
		Types:   make([]commsTypeResponse, 0, len(v.Types)),
		Forms:   make([]formCompletionResponse, 0, len(v.Forms)),
		Funnels: v.Funnels,
		Period:  optionalPeriodResponse{From: v.Period.From, To: v.Period.To},
	}
	for _, t := range v.Types {
		out.Types = append(out.Types, toCommsType(t))
	}
	for _, f := range v.Forms {
		out.Forms = append(out.Forms, formCompletionResponse{
			ID: f.ID, Title: f.Title, Registration: f.Registration, Enabled: f.Enabled,
			Assigned: f.Assigned, Completed: f.Completed, Answers: f.Answers, CompletionRate: f.CompletionRate,
		})
	}
	return out
}

// exportCommunications godoc
// @Summary Export one table of the communications analytics as CSV (UTF-8 with BOM)
// @Description table is types or forms.
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param table query string true "types | forms"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/communications/export.csv [get]
func (h *Handler) exportCommunications(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	table := ctx.Query("table")
	if table != "types" && table != "forms" {
		response.AbortWithBadRequest(ctx, errors.New("unknown table"))
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsCommunications(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startAnalyticsCSV(ctx, "communications-"+table)
	n := func(x int64) string { return strconv.FormatInt(x, 10) }
	rate := func(r *float64) string {
		if r == nil {
			return ""
		}
		return strconv.FormatFloat(*r, 'f', 3, 64)
	}
	if table == "types" {
		_ = w.Write([]string{"Тип", "Листів надіслано", "Листів з помилкою", "Сповіщень надіслано", "Сповіщень з помилкою", "Сповіщень створено", "Прочитано", "Частка прочитаних"})
		for _, t := range v.Types {
			_ = w.Write([]string{csvCellText(t.Type), n(t.EmailSent), n(t.EmailErrors), n(t.InAppSent), n(t.InAppErrors), n(t.InAppCreated), n(t.InAppRead), rate(t.ReadRate)})
		}
	} else {
		_ = w.Write([]string{"Форма", "Призначено", "Заповнено", "Відповідей", "Частка заповнених"})
		for _, f := range v.Forms {
			_ = w.Write([]string{csvCellText(f.Title), n(f.Assigned), n(f.Completed), n(f.Answers), rate(f.CompletionRate)})
		}
	}
	w.Flush()
}
