// Package dashboard is the HTTP delivery of the platform admin dashboard: GET /api/admin/dashboard.
package dashboard

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminDashboard "github.com/cybericebox/daemon/internal/useCase/adminDashboard"
)

type (
	IUseCase interface {
		GetAdminDashboard(ctx context.Context) (adminDashboard.View, error)
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	attentionItemResponse struct {
		ID    uuid.UUID  `json:"ID"`
		Label string     `json:"Label"`
		Count int64      `json:"Count"`
		At    *time.Time `json:"At"`
	}

	// attentionResponse: Kind is failed_stands, events_without_reservation, pending_elevations, open_errors or
	// mail_errors; Target names the place that fixes it (stands, reservations, elevations, errors, mail); Items are
	// the first few rows (events or error groups).
	attentionResponse struct {
		Kind   string                  `json:"Kind"`
		Count  int64                   `json:"Count"`
		Target string                  `json:"Target"`
		Items  []attentionItemResponse `json:"Items"`
	}

	currentEventResponse struct {
		EventID      uuid.UUID  `json:"EventID"`
		Name         string     `json:"Name"`
		Tag          string     `json:"Tag"`
		StartAt      time.Time  `json:"StartAt"`
		FinishAt     *time.Time `json:"FinishAt"`
		Running      bool       `json:"Running"`
		Participants int64      `json:"Participants"`
		Teams        int64      `json:"Teams"`
		StandsReady  int64      `json:"StandsReady"`
		StandsTotal  int64      `json:"StandsTotal"`
	}

	// trendsResponse: the series are aligned with Days; a series the caller has no right to is null.
	trendsResponse struct {
		Days          []time.Time `json:"Days"`
		Registrations []int64     `json:"Registrations"`
		ActiveUsers   []int64     `json:"ActiveUsers"`
		Solves        []int64     `json:"Solves"`
	}

	infrastructureResponse struct {
		CPUAllocatableMillicores int64 `json:"CPUAllocatableMillicores"`
		CPUReservedMillicores    int64 `json:"CPUReservedMillicores"`
		MemoryAllocatableBytes   int64 `json:"MemoryAllocatableBytes"`
		MemoryReservedBytes      int64 `json:"MemoryReservedBytes"`
		Agents                   int64 `json:"Agents"`
		ActiveStands             int64 `json:"ActiveStands"`
		FailedStands             int64 `json:"FailedStands"`
	}

	// dashboardResponse: a block the caller has no permission for is null; Attention is always a list (empty when
	// nothing needs attention or nothing may be seen).
	dashboardResponse struct {
		Attention          []attentionResponse     `json:"Attention"`
		CurrentAndUpcoming []currentEventResponse  `json:"CurrentAndUpcoming"`
		Trends7d           *trendsResponse         `json:"Trends7d"`
		Infrastructure     *infrastructureResponse `json:"Infrastructure"`
	}
)

func New(
	useCase IUseCase,
	prot IProtection,
) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	// The admin tier passes the gate; each block then checks its own permission.
	router.GET(
		"admin/dashboard",
		h.prot.RequirePermission(rbac.PermSelf),
		h.requireAdminTier,
		h.get,
	)
}

// requireAdminTier lets only the admin roles in: an ordinary account has no dashboard.
func (h *Handler) requireAdminTier(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok || !claims.Role.IsAdminTier() {
		response.AbortWithForbidden(ctx)
		return
	}
	ctx.Next()
}

// get godoc
// @Summary  The platform admin dashboard
// @Description One answer with the blocks the caller may see: Attention (what needs the admin, with where to fix it), CurrentAndUpcoming (running events and those starting within 7 days), Trends7d (sign-ups, active users, solves) and Infrastructure (cluster CPU and memory, active stands). A block without the permission is null.
// @Tags     admin
// @Produce  json
// @Success  200  {object}  response.Response{data=dashboardResponse}
// @Router   /admin/dashboard [get]
func (h *Handler) get(ctx *gin.Context) {
	view, err := h.useCase.GetAdminDashboard(ctx.Request.Context())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, toResponse(view))
}

func toResponse(v adminDashboard.View) dashboardResponse {
	out := dashboardResponse{Attention: make([]attentionResponse, 0, len(v.Attention))}
	for _, a := range v.Attention {
		item := attentionResponse{
			Kind:   a.Kind,
			Count:  a.Count,
			Target: a.Target,
			Items:  make([]attentionItemResponse, 0, len(a.Items)),
		}
		for _, i := range a.Items {
			item.Items = append(
				item.Items,
				attentionItemResponse{ID: i.ID, Label: i.Label, Count: i.Count, At: i.At},
			)
		}
		out.Attention = append(out.Attention, item)
	}
	if v.CurrentAndUpcoming != nil {
		out.CurrentAndUpcoming = make([]currentEventResponse, 0, len(v.CurrentAndUpcoming))
		for _, e := range v.CurrentAndUpcoming {
			out.CurrentAndUpcoming = append(
				out.CurrentAndUpcoming,
				currentEventResponse{
					EventID:      e.ID,
					Name:         e.Name,
					Tag:          e.Tag,
					StartAt:      e.StartAt,
					FinishAt:     e.FinishAt,
					Running:      e.Running,
					Participants: e.Participants,
					Teams:        e.Teams,
					StandsReady:  e.StandsReady,
					StandsTotal:  e.StandsTotal,
				},
			)
		}
	}
	if v.Trends7d != nil {
		out.Trends7d = &trendsResponse{
			Days:          v.Trends7d.Days,
			Registrations: v.Trends7d.Registrations,
			ActiveUsers:   v.Trends7d.ActiveUsers,
			Solves:        v.Trends7d.Solves,
		}
	}
	if i := v.Infrastructure; i != nil {
		out.Infrastructure = &infrastructureResponse{
			CPUAllocatableMillicores: i.CPUAllocatableMillicores,
			CPUReservedMillicores:    i.CPUReservedMillicores,
			MemoryAllocatableBytes:   i.MemoryAllocatableBytes,
			MemoryReservedBytes:      i.MemoryReservedBytes,
			Agents:                   i.Agents,
			ActiveStands:             i.ActiveStands,
			FailedStands:             i.FailedStands,
		}
	}
	return out
}
