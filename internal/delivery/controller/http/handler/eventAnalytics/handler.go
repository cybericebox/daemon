// Package eventAnalytics is the HTTP delivery of event analytics
// (docs/EVENT-ANALYTICS.md): GET /events/{id}/manage/analytics/*. Every
// route is gated twice: PermSelf on the group, then the §7 analytics access
// of the viewer for this event (requireSections / requireSensitive).
package eventAnalytics

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		RequireEventAnalytics(ctx context.Context, eventID uuid.UUID, viewer rbac.Claims, level eventAnalyticsUseCase.Level) error
		EventAnalyticsAccess(ctx context.Context, eventID uuid.UUID, viewer rbac.Claims) (eventAnalyticsModel.Access, error)
		TasksUseCase
		StandsUseCase
		UsageUseCase
		IntegrityUseCase
		ReportUseCase
		ParticipantsUseCase
		ProgressUseCase
		GetEventAnalyticsOverview(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.OverviewView, error)
	}
)

func NewEventAnalyticsAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	{
		analytics.GET("access", h.requireSections, h.access)
		analytics.GET("overview", h.requireSections, h.overview)
		analytics.GET("overview/export.csv", h.requireSections, h.exportOverview)
		h.initTasks(router)
		h.initStands(router)
		h.initUsage(router)
		h.initIntegrity(router)
		h.initReport(router)
		h.initParticipants(router)
		h.initProgress(router)
	}
}

// requireSections admits the event staff who may see the analytics sections
// (§7: owner, every moderator, platform staff).
func (h *Handler) requireSections(ctx *gin.Context) {
	h.require(ctx, eventAnalyticsUseCase.LevelSections)
}

// requireSensitive admits only the viewers of wrong answers and integrity
// signals (§7: owner, write moderators, platform admins).
func (h *Handler) requireSensitive(ctx *gin.Context) {
	h.require(ctx, eventAnalyticsUseCase.LevelSensitive)
}

func (h *Handler) require(ctx *gin.Context, level eventAnalyticsUseCase.Level) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := h.useCase.RequireEventAnalytics(ctx, eventID, claims, level); err != nil {
		response.AbortWithError(ctx, err)
	}
}

type accessResponse struct {
	// Sections: every analytics section and the event report.
	Sections bool `json:"Sections"`
	// Sensitive: wrong answer texts and the integrity section.
	Sensitive bool `json:"Sensitive"`
}

// access godoc
// @Summary What the caller may see in this event's analytics (§7)
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=accessResponse}
// @Router /events/{id}/manage/analytics/access [get]
func (h *Handler) access(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	access, err := h.useCase.EventAnalyticsAccess(ctx, eventID, claims)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, accessResponse{Sections: access.Sections, Sensitive: access.Sensitive})
}

func parseEventID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

// parsePeriod reads the optional from/to query parameters (RFC 3339).
func parsePeriod(ctx *gin.Context) (from, to *time.Time, ok bool) {
	parse := func(name string) (*time.Time, bool) {
		value := ctx.Query(name)
		if value == "" {
			return nil, true
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return nil, false
		}
		return &parsed, true
	}
	if from, ok = parse("from"); !ok {
		return nil, nil, false
	}
	if to, ok = parse("to"); !ok {
		return nil, nil, false
	}
	return from, to, true
}
