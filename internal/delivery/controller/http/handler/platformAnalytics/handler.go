// Package platformAnalytics is the HTTP delivery of platform-level analytics
// (docs/EVENT-ANALYTICS.md §8): GET /api/analytics/<section>. Every route is
// gated by RequirePermission — analytics.read for the aggregates, and
// analytics.users.read (super_admin only) for the per-user rows. No IP
// addresses and no answer texts are ever returned.
package platformAnalytics

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	// IUseCase is the union of the per-section use case ports.
	IUseCase interface {
		OverviewUseCase
		UsersUseCase
		EventsUseCase
		TasksUseCase
		InfrastructureUseCase
		MailUseCase
	}
)

func NewPlatformAnalyticsAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	h.initOverview(router)
	h.initUsers(router)
	h.initEvents(router)
	h.initTasks(router)
	h.initInfrastructure(router)
	h.initMail(router)
}

// staff gates the aggregate routes: super_admin and any role that holds
// analytics.read.
func (h *Handler) staff() gin.HandlerFunc {
	return h.prot.RequirePermission(rbac.PermAnalyticsRead)
}

type periodResponse struct {
	From time.Time `json:"From"`
	To   time.Time `json:"To"`
	// All: the caller asked for all time (no lower bound).
	All bool `json:"All"`
}

// parsePeriod reads the optional from/to query parameters (RFC 3339); a
// missing `from` means all time.
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
