package eventAnalytics

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// ParticipantsUseCase is the «Учасники» and «Комунікації» part of IUseCase.
type ParticipantsUseCase interface {
	GetEventAnalyticsParticipants(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.ParticipantsView, error)
	GetEventAnalyticsCommunications(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.CommunicationsView, error)
}

// initParticipants registers the «Учасники» and «Комунікації» routes. The
// reports hold no wrong answers, so every route needs only the sections level.
// The group carries its own PermSelf gate: tools/checkroutes sees only gates
// written in the registering function.
func (h *Handler) initParticipants(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("participants", h.requireSections, h.participants)
	analytics.GET("participants/export.csv", h.requireSections, h.exportParticipants)
	analytics.GET("communications", h.requireSections, h.communications)
	analytics.GET("communications/export.csv", h.requireSections, h.exportCommunications)
}

type (
	participantsResponse struct {
		// TeamMode is false for an individual event (no team stage or fill).
		TeamMode      bool                   `json:"TeamMode"`
		Funnel        []funnelStageResponse  `json:"Funnel"`
		Registrations registrationsResponse  `json:"Registrations"`
		Teams         teamFillResponse       `json:"Teams"`
		Answers       answersResponse        `json:"Answers"`
		DropOff       dropOffResponse        `json:"DropOff"`
		Period        optionalPeriodResponse `json:"Period"`
	}

	// optionalPeriodResponse is a window whose bounds may be open (null).
	optionalPeriodResponse struct {
		From *time.Time `json:"From"`
		To   *time.Time `json:"To"`
	}

	// funnelStageResponse: Stage is invited, registered, approved, in_team
	// (team events only), attempted or solved.
	funnelStageResponse struct {
		Stage string `json:"Stage"`
		Count int64  `json:"Count"`
	}

	registrationsResponse struct {
		// Days has an entry per UTC day.
		Days  []registrationDayResponse `json:"Days"`
		Total int64                     `json:"Total"`
	}

	registrationDayResponse struct {
		Day        time.Time `json:"Day"`
		Open       int64     `json:"Open"`
		Approval   int64     `json:"Approval"`
		Invitation int64     `json:"Invitation"`
	}

	teamFillResponse struct {
		MinSize int32 `json:"MinSize"`
		MaxSize int32 `json:"MaxSize"`
		Total   int64 `json:"Total"`
		// Histogram has an entry per team size from 0 to MaxSize.
		Histogram  []fillBucketResponse     `json:"Histogram"`
		Incomplete []incompleteTeamResponse `json:"Incomplete"`
		// PendingInvitees were invited into a team and have not joined.
		PendingInvitees int64 `json:"PendingInvitees"`
		// WithoutTeam are approved participants in no team.
		WithoutTeam int64 `json:"WithoutTeam"`
	}

	fillBucketResponse struct {
		Members int32 `json:"Members"`
		Teams   int64 `json:"Teams"`
	}

	incompleteTeamResponse struct {
		ID              uuid.UUID `json:"ID"`
		Name            string    `json:"Name"`
		Members         int32     `json:"Members"`
		PendingInvitees int64     `json:"PendingInvitees"`
	}

	answersResponse struct {
		Respondents int64              `json:"Respondents"`
		Questions   []questionResponse `json:"Questions"`
	}

	// questionResponse: Input picks the chart. select, multi_select, checkbox
	// (labels yes/no): bars; number: histogram with Min/Max/Avg; date:
	// timeline (labels are days, months or hours); text, long_text: the
	// repeated values (Distinct counts all different ones); file: has/none.
	questionResponse struct {
		Key      string           `json:"Key"`
		Label    string           `json:"Label"`
		Input    string           `json:"Input"`
		Asked    int64            `json:"Asked"`
		Answered int64            `json:"Answered"`
		Distinct int64            `json:"Distinct"`
		Buckets  []bucketResponse `json:"Buckets"`
		Min      *float64         `json:"Min"`
		Max      *float64         `json:"Max"`
		Avg      *float64         `json:"Avg"`
	}

	bucketResponse struct {
		Label string `json:"Label"`
		Count int64  `json:"Count"`
	}

	dropOffResponse struct {
		// Total may exceed the number of Rows: the list is cut.
		Total int64                `json:"Total"`
		Rows  []dropOffRowResponse `json:"Rows"`
	}

	dropOffRowResponse struct {
		UserID       uuid.UUID  `json:"UserID"`
		Name         string     `json:"Name"`
		Email        string     `json:"Email"`
		TeamName     string     `json:"TeamName"`
		RegisteredAt time.Time  `json:"RegisteredAt"`
		ApprovedAt   *time.Time `json:"ApprovedAt"`
		OpenedTasks  int64      `json:"OpenedTasks"`
	}
)

// participants godoc
// @Summary Participants and registration analytics
// @Description §6.2 «Учасники й реєстрація»: the participation funnel, registrations per day and channel, team fill, registration form answer distributions and the drop-off list. from/to limit only the registrations per day (open bounds by default); the rest is the current state.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {object} response.Response{data=participantsResponse}
// @Router /events/{id}/manage/analytics/participants [get]
func (h *Handler) participants(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsParticipants(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantsResponse(v))
}

func toParticipantsResponse(v eventAnalyticsUseCase.ParticipantsView) participantsResponse {
	out := participantsResponse{
		TeamMode:      v.TeamMode,
		Funnel:        make([]funnelStageResponse, 0, len(v.Funnel)),
		Registrations: registrationsResponse{Days: make([]registrationDayResponse, 0, len(v.Registrations.Days)), Total: v.Registrations.Total},
		Teams: teamFillResponse{
			MinSize: v.Teams.MinSize, MaxSize: v.Teams.MaxSize, Total: v.Teams.Total, PendingInvitees: v.Teams.PendingInvitees,
			WithoutTeam: v.Teams.WithoutTeam, Histogram: make([]fillBucketResponse, 0, len(v.Teams.Histogram)),
			Incomplete: make([]incompleteTeamResponse, 0, len(v.Teams.Incomplete)),
		},
		Answers: answersResponse{Respondents: v.Answers.Respondents, Questions: make([]questionResponse, 0, len(v.Answers.Questions))},
		DropOff: dropOffResponse{Total: v.DropOff.Total, Rows: make([]dropOffRowResponse, 0, len(v.DropOff.Rows))},
		Period:  optionalPeriodResponse{From: v.Period.From, To: v.Period.To},
	}
	for _, s := range v.Funnel {
		out.Funnel = append(out.Funnel, funnelStageResponse{Stage: s.Stage, Count: s.Count})
	}
	for _, d := range v.Registrations.Days {
		out.Registrations.Days = append(out.Registrations.Days, registrationDayResponse{Day: d.Day, Open: d.Open, Approval: d.Approval, Invitation: d.Invitation})
	}
	for _, b := range v.Teams.Histogram {
		out.Teams.Histogram = append(out.Teams.Histogram, fillBucketResponse{Members: b.Members, Teams: b.Teams})
	}
	for _, t := range v.Teams.Incomplete {
		out.Teams.Incomplete = append(out.Teams.Incomplete, incompleteTeamResponse{ID: t.ID, Name: t.Name, Members: t.Members, PendingInvitees: t.PendingInvitees})
	}
	for _, q := range v.Answers.Questions {
		buckets := make([]bucketResponse, 0, len(q.Buckets))
		for _, b := range q.Buckets {
			buckets = append(buckets, bucketResponse{Label: b.Label, Count: b.Count})
		}
		out.Answers.Questions = append(out.Answers.Questions, questionResponse{
			Key: q.Key, Label: q.Label, Input: q.Input, Asked: q.Asked, Answered: q.Answered, Distinct: q.Distinct,
			Buckets: buckets, Min: q.Min, Max: q.Max, Avg: q.Avg,
		})
	}
	for _, r := range v.DropOff.Rows {
		out.DropOff.Rows = append(out.DropOff.Rows, dropOffRowResponse{
			UserID: r.UserID, Name: r.Name, Email: r.Email, TeamName: r.TeamName,
			RegisteredAt: r.RegisteredAt, ApprovedAt: r.ApprovedAt, OpenedTasks: r.OpenedTasks,
		})
	}
	return out
}

// exportParticipants godoc
// @Summary Export one table of the participants analytics as CSV (UTF-8 with BOM)
// @Description table is funnel, registrations, teams, answers or dropoff.
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param table query string true "funnel | registrations | teams | answers | dropoff"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/participants/export.csv [get]
func (h *Handler) exportParticipants(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	table := ctx.Query("table")
	switch table {
	case "funnel", "registrations", "teams", "answers", "dropoff":
	default:
		response.AbortWithBadRequest(ctx, errors.New("unknown table"))
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsParticipants(ctx, eventID, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startAnalyticsCSV(ctx, "participants-"+table)
	n := func(x int64) string { return strconv.FormatInt(x, 10) }
	switch table {
	case "funnel":
		_ = w.Write([]string{"Етап", "Учасників"})
		for _, s := range v.Funnel {
			_ = w.Write([]string{funnelStageLabels[s.Stage], n(s.Count)})
		}
	case "registrations":
		_ = w.Write([]string{"День (UTC)", "Відкрита реєстрація", "Схвалення", "Запрошення"})
		for _, d := range v.Registrations.Days {
			_ = w.Write([]string{d.Day.UTC().Format("2006-01-02"), n(d.Open), n(d.Approval), n(d.Invitation)})
		}
	case "teams":
		_ = w.Write([]string{"Команда", "Учасників", "Очікують приєднання"})
		for _, t := range v.Teams.Incomplete {
			_ = w.Write([]string{csvCellText(t.Name), strconv.Itoa(int(t.Members)), n(t.PendingInvitees)})
		}
	case "answers":
		_ = w.Write([]string{"Питання", "Значення", "Відповідей"})
		for _, q := range v.Answers.Questions {
			for _, b := range q.Buckets {
				_ = w.Write([]string{csvCellText(q.Label), csvCellText(b.Label), n(b.Count)})
			}
		}
	case "dropoff":
		_ = w.Write([]string{"Учасник", "Пошта", "Команда", "Зареєстрований (UTC)", "Схвалений (UTC)", "Відкрито завдань"})
		for _, r := range v.DropOff.Rows {
			registered := r.RegisteredAt
			_ = w.Write([]string{csvCellText(r.Name), csvCellText(r.Email), csvCellText(r.TeamName), csvTimePtr(&registered), csvTimePtr(r.ApprovedAt), n(r.OpenedTasks)})
		}
	}
	w.Flush()
}

var funnelStageLabels = map[string]string{
	eventAnalyticsUseCase.StageInvited:    "Запрошено",
	eventAnalyticsUseCase.StageRegistered: "Зареєстровано",
	eventAnalyticsUseCase.StageApproved:   "Схвалено",
	eventAnalyticsUseCase.StageInTeam:     "У команді",
	eventAnalyticsUseCase.StageAttempted:  "Перша спроба",
	eventAnalyticsUseCase.StageSolved:     "Перше розв'язання",
}
