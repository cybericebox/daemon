package platformAnalytics

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// EventsUseCase is the port of the events section.
type EventsUseCase interface {
	GetEvents(ctx context.Context, from, to *time.Time) (platformAnalyticsUseCase.EventsView, error)
}

func (h *Handler) initEvents(router *gin.RouterGroup) {
	analytics := router.Group("analytics", h.prot.RequirePermission(rbac.PermAnalyticsRead))
	analytics.GET("events", h.events)
	analytics.GET("events/export.csv", h.exportEvents)
}

type (
	eventsResponse struct {
		Period      periodResponse          `json:"Period"`
		Totals      eventsTotalsResponse    `json:"Totals"`
		Series      []eventDayResponse      `json:"Series"`
		Statuses    []eventStatusResponse   `json:"Statuses"`
		Events      []eventRowResponse      `json:"Events"`
		EventsTotal int64                   `json:"EventsTotal"`
		EventsLimit int                     `json:"EventsLimit"`
		Upcoming    []upcomingEventResponse `json:"Upcoming"`
	}

	eventsTotalsResponse struct {
		Events        int64 `json:"Events"`
		Created       int64 `json:"Created"`
		Started       int64 `json:"Started"`
		Registrations int64 `json:"Registrations"`
	}

	eventDayResponse struct {
		Day           time.Time `json:"Day"`
		EventsCreated int64     `json:"EventsCreated"`
		EventsStarted int64     `json:"EventsStarted"`
		Registrations int64     `json:"Registrations"`
	}

	eventStatusResponse struct {
		// Status: not_published, published, started, finished, withdrawn.
		Status string `json:"Status"`
		Events int64  `json:"Events"`
	}

	// eventRowResponse: CompletionRate is the share of teams with at least one
	// solve (0..1); the duration is seconds, null until the event has a finish;
	// StartAt is null for an event that was never scheduled.
	eventRowResponse struct {
		ID              uuid.UUID  `json:"ID"`
		Tag             string     `json:"Tag"`
		Name            string     `json:"Name"`
		Status          string     `json:"Status"`
		StartAt         *time.Time `json:"StartAt"`
		FinishAt        *time.Time `json:"FinishAt"`
		DurationSeconds *int64     `json:"DurationSeconds"`
		Participants    int64      `json:"Participants"`
		Teams           int64      `json:"Teams"`
		Solves          int64      `json:"Solves"`
		TeamsSolved     int64      `json:"TeamsSolved"`
		CompletionRate  float64    `json:"CompletionRate"`
	}

	upcomingEventResponse struct {
		ID            uuid.UUID `json:"ID"`
		Tag           string    `json:"Tag"`
		Name          string    `json:"Name"`
		StartAt       time.Time `json:"StartAt"`
		Published     bool      `json:"Published"`
		Registrations int64     `json:"Registrations"`
	}
)

func toEventsResponse(v platformAnalyticsUseCase.EventsView) eventsResponse {
	out := eventsResponse{
		Period:      periodResponse{From: v.Period.From, To: v.Period.To, All: v.Period.All},
		Totals:      eventsTotalsResponse(v.Totals),
		Series:      make([]eventDayResponse, 0, len(v.Series)),
		Statuses:    make([]eventStatusResponse, 0, len(v.Statuses)),
		Events:      make([]eventRowResponse, 0, len(v.Events)),
		EventsTotal: v.EventsTotal,
		EventsLimit: v.EventsLimit,
		Upcoming:    make([]upcomingEventResponse, 0, len(v.Upcoming)),
	}
	for _, d := range v.Series {
		out.Series = append(out.Series, eventDayResponse(d))
	}
	for _, s := range v.Statuses {
		out.Statuses = append(out.Statuses, eventStatusResponse(s))
	}
	for _, e := range v.Events {
		row := eventRowResponse{
			ID: e.ID, Tag: e.Tag, Name: e.Name, Status: e.Status, FinishAt: e.FinishAt, DurationSeconds: e.DurationSeconds,
			Participants: e.Participants, Teams: e.Teams, Solves: e.Solves, TeamsSolved: e.TeamsSolved, CompletionRate: e.CompletionRate,
		}
		if !e.StartAt.IsZero() {
			start := e.StartAt
			row.StartAt = &start
		}
		out.Events = append(out.Events, row)
	}
	for _, n := range v.Upcoming {
		out.Upcoming = append(out.Upcoming, upcomingEventResponse(n))
	}
	return out
}

// events godoc
// @Summary Platform analytics: events
// @Description Events created and started and registrations per day of the period, events by lifecycle status, the newest events of the period with participants, teams, solves, completion rate and duration (at most EventsLimit; EventsTotal is the full count), and the next scheduled events. Aggregates only, the moderators team is not counted.
// @Tags platform-analytics
// @Produce json
// @Param from query string false "period start (RFC 3339); omitted means all time"
// @Param to query string false "period end, exclusive (RFC 3339); omitted means now"
// @Success 200 {object} response.Response{data=eventsResponse}
// @Router /analytics/events [get]
func (h *Handler) events(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEvents(ctx, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventsResponse(v))
}

// exportEvents godoc
// @Summary Export a table of the events analytics as CSV (UTF-8 with BOM)
// @Description table=events (the per-event table), table=series (per day) or table=upcoming (next scheduled events).
// @Tags platform-analytics
// @Produce text/csv
// @Param table query string true "events, series or upcoming"
// @Param from query string false "period start (RFC 3339); omitted means all time"
// @Param to query string false "period end, exclusive (RFC 3339); omitted means now"
// @Success 200 {string} string "CSV"
// @Router /analytics/events/export.csv [get]
func (h *Handler) exportEvents(ctx *gin.Context) {
	table := ctx.Query("table")
	switch table {
	case "events", "series", "upcoming":
	default:
		response.AbortWithError(ctx, platformAnalyticsModel.ErrPlatformAnalyticsTableUnknown.Err())
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEvents(ctx, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "events-"+table)
	switch table {
	case "series":
		_ = w.Write([]string{"День (UTC)", "Створено заходів", "Стартувало заходів", "Реєстрацій"})
		for _, d := range v.Series {
			_ = w.Write([]string{csvDay(d.Day), csvInt(d.EventsCreated), csvInt(d.EventsStarted), csvInt(d.Registrations)})
		}
	case "upcoming":
		_ = w.Write([]string{"Захід", "Тег", "Старт (UTC)", "Опубліковано", "Реєстрацій"})
		for _, n := range v.Upcoming {
			_ = w.Write([]string{csvText(n.Name), csvText(n.Tag), csvTime(n.StartAt), csvBool(n.Published), csvInt(n.Registrations)})
		}
	default:
		_ = w.Write([]string{"Захід", "Тег", "Статус", "Старт (UTC)", "Фініш (UTC)", "Учасників", "Команд", "Розвʼязань", "Команд із розвʼязанням", "Частка команд із розвʼязанням", "Тривалість (с)"})
		for _, e := range v.Events {
			start := ""
			if !e.StartAt.IsZero() {
				start = csvTime(e.StartAt)
			}
			duration := ""
			if e.DurationSeconds != nil {
				duration = csvInt(*e.DurationSeconds)
			}
			_ = w.Write([]string{csvText(e.Name), csvText(e.Tag), e.Status, start, csvTimePtr(e.FinishAt), csvInt(e.Participants), csvInt(e.Teams),
				csvInt(e.Solves), csvInt(e.TeamsSolved), csvFloat(e.CompletionRate), duration})
		}
	}
	w.Flush()
}

func csvBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
