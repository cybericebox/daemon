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

// UsersUseCase is the port of the users section.
type UsersUseCase interface {
	GetUsers(ctx context.Context, from, to *time.Time) (platformAnalyticsUseCase.UsersView, error)
	// GetUsersPeople holds personal data: only behind analytics.users.read.
	GetUsersPeople(ctx context.Context) ([]platformAnalyticsUseCase.UsersPersonView, error)
}

type (
	usersResponse struct {
		Period         overviewPeriodResponse   `json:"Period"`
		Total          int64                    `json:"Total"`
		Blocked        int64                    `json:"Blocked"`
		ByRole         []usersRoleResponse      `json:"ByRole"`
		New            overviewMetricResponse   `json:"New"`
		Active         overviewMetricResponse   `json:"Active"`
		AvgDailyActive float64                  `json:"AvgDailyActive"`
		Registrations  []overviewDayNewResponse `json:"Registrations"`
		ActiveByDay    []usersActiveDayResponse `json:"ActiveByDay"`
		Methods        []usersMethodResponse    `json:"Methods"`
		Retention      usersRetentionResponse   `json:"Retention"`
	}

	usersRoleResponse struct {
		Role  string `json:"Role"`
		Count int64  `json:"Count"`
	}

	usersActiveDayResponse struct {
		Day time.Time `json:"Day"`
		DAU int64     `json:"DAU"`
		WAU int64     `json:"WAU"`
	}

	// usersMethodResponse: Method is password, google, both or none (exclusive
	// groups); Total are all current accounts, New those registered in the period.
	usersMethodResponse struct {
		Method string `json:"Method"`
		Total  int64  `json:"Total"`
		New    int64  `json:"New"`
	}

	usersRetentionResponse struct {
		One       int64 `json:"One"`
		Two       int64 `json:"Two"`
		ThreePlus int64 `json:"ThreePlus"`
		Never     int64 `json:"Never"`
	}

	usersPersonResponse struct {
		ID           uuid.UUID `json:"ID"`
		Name         string    `json:"Name"`
		Email        string    `json:"Email"`
		Role         string    `json:"Role"`
		EventsJoined int64     `json:"EventsJoined"`
		Solves       int64     `json:"Solves"`
		LastSeenAt   time.Time `json:"LastSeenAt"`
	}
)

func (h *Handler) initUsers(router *gin.RouterGroup) {
	group := router.Group("analytics", h.prot.RequirePermission(rbac.PermAnalyticsRead))
	group.GET("users", h.users)
	group.GET("users/export.csv", h.exportUsers)

	// Per-user rows: super_admin only (analytics.users.read).
	people := router.Group("analytics/users/people", h.prot.RequirePermission(rbac.PermAnalyticsUsersRead))
	people.GET("", h.usersPeople)
}

func toUsersResponse(v platformAnalyticsUseCase.UsersView) usersResponse {
	out := usersResponse{
		Period: toOverviewPeriodResponse(v.Period), Total: v.Total, Blocked: v.Blocked,
		New: toOverviewMetricResponse(v.New), Active: toOverviewMetricResponse(v.Active), AvgDailyActive: v.AvgDailyActive,
		ByRole:        make([]usersRoleResponse, 0, len(v.ByRole)),
		Registrations: make([]overviewDayNewResponse, 0, len(v.Registrations)),
		ActiveByDay:   make([]usersActiveDayResponse, 0, len(v.ActiveByDay)),
		Methods:       make([]usersMethodResponse, 0, len(v.Methods)),
		Retention:     usersRetentionResponse{One: v.Retention.One, Two: v.Retention.Two, ThreePlus: v.Retention.ThreePlus, Never: v.Retention.Never},
	}
	for _, r := range v.ByRole {
		out.ByRole = append(out.ByRole, usersRoleResponse{Role: r.Role, Count: r.Count})
	}
	for _, d := range v.Registrations {
		out.Registrations = append(out.Registrations, overviewDayNewResponse{Day: d.Day, New: d.New})
	}
	for _, d := range v.ActiveByDay {
		out.ActiveByDay = append(out.ActiveByDay, usersActiveDayResponse{Day: d.Day, DAU: d.DAU, WAU: d.WAU})
	}
	for _, m := range v.Methods {
		out.Methods = append(out.Methods, usersMethodResponse{Method: m.Method, Total: m.Total, New: m.New})
	}
	return out
}

func toUsersPeopleResponse(rows []platformAnalyticsUseCase.UsersPersonView) []usersPersonResponse {
	out := make([]usersPersonResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, usersPersonResponse{ID: r.ID, Name: r.Name, Email: r.Email, Role: r.Role, EventsJoined: r.EventsJoined, Solves: r.Solves, LastSeenAt: r.LastSeenAt})
	}
	return out
}

// users godoc
// @Summary Platform users analytics
// @Description Aggregates of the admin user stats bound to the period (total, blocked, by role, new and active accounts, registrations per day) plus daily and weekly active accounts, the sign-in method split (password, Google, both, none) and how many events accounts joined (1, 2, 3+). Aggregates only; per-user rows are at /analytics/users/people. from/to are RFC 3339; no from means all time.
// @Tags platform-analytics
// @Produce json
// @Param from query string false "period start (RFC 3339); absent = all time"
// @Param to query string false "period end, exclusive (RFC 3339); absent = now"
// @Success 200 {object} response.Response{data=usersResponse}
// @Router /analytics/users [get]
func (h *Handler) users(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetUsers(ctx, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toUsersResponse(v))
}

// usersPeople godoc
// @Summary The most active users (per-user rows)
// @Description Up to 100 accounts with the most events joined (name, email, role, events joined, solves). Personal data: requires analytics.users.read (super_admin).
// @Tags platform-analytics
// @Produce json
// @Success 200 {object} response.Response{data=[]usersPersonResponse}
// @Router /analytics/users/people [get]
func (h *Handler) usersPeople(ctx *gin.Context) {
	rows, err := h.useCase.GetUsersPeople(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toUsersPeopleResponse(rows))
}

// exportUsers godoc
// @Summary Export one table of the users analytics as CSV (UTF-8 with BOM)
// @Description table is registrations, activity, methods, retention, or people (per-user rows: requires analytics.users.read).
// @Tags platform-analytics
// @Produce text/csv
// @Param table query string true "registrations | activity | methods | retention | people"
// @Param from query string false "period start (RFC 3339); absent = all time"
// @Param to query string false "period end, exclusive (RFC 3339); absent = now"
// @Success 200 {string} string "CSV"
// @Router /analytics/users/export.csv [get]
func (h *Handler) exportUsers(ctx *gin.Context) {
	table := ctx.Query("table")
	switch table {
	case "registrations", "activity", "methods", "retention":
	case "people":
		// The people export is per-user data: the same gate as the table.
		h.prot.RequirePermission(rbac.PermAnalyticsUsersRead)(ctx)
		if ctx.IsAborted() {
			return
		}
		rows, err := h.useCase.GetUsersPeople(ctx)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		w := startCSV(ctx, "users-people")
		writeUsersPeople(w, rows)
		w.Flush()
		return
	default:
		response.AbortWithError(ctx, platformAnalyticsModel.ErrPlatformAnalyticsTableUnknown.Err())
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetUsers(ctx, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "users-"+table)
	switch table {
	case "registrations":
		writeUsersRegistrations(w, v)
	case "activity":
		writeUsersActivity(w, v)
	case "methods":
		writeUsersMethods(w, v)
	case "retention":
		writeUsersRetention(w, v)
	}
	w.Flush()
}
