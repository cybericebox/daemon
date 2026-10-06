package eventself

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func contentAccess(ctx *gin.Context) eventUseCase.ContentAccess {
	access := eventUseCase.ContentAccess{Role: rbac.RolePublic}
	if claims, found := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); found {
		access.UserID, access.Role = &claims.UserID, claims.Role
	}
	return access
}

// publicContent godoc
// @Summary Read published event landing content
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=publicEventContentResponse}
// @Router /events/{id}/content [get]
func (h *Handler) publicContent(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	content, err := h.useCase.GetPublicEventContent(ctx, eventID, contentAccess(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toPublicEventContentResponse(content))
}

func (h *Handler) publicContentDocument(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	content, err := h.useCase.GetPublicEventContent(ctx, eventID, eventUseCase.ContentAccess{Role: rbac.RolePublic})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, struct {
		Landing any `json:"Landing"`
	}{Landing: content.Landing})
}

func (h *Handler) publicContentValues(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	content, err := h.useCase.GetPublicEventContent(ctx, eventID, eventUseCase.ContentAccess{Role: rbac.RolePublic})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, struct {
		Variables map[string]any `json:"Variables"`
	}{Variables: content.Variables})
}

func (h *Handler) visiblePages(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	pages, err := h.useCase.ListNavigationPages(ctx, eventID, contentAccess(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, pages)
}

// publicPage godoc
// @Summary Read one visible static event page
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param slug path string true "page slug"
// @Success 200 {object} response.Response{data=publicEventPageResponse}
// @Router /events/{id}/content/pages/{slug} [get]
func (h *Handler) publicPage(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.GetPublicEventPage(ctx, eventID, ctx.Param("slug"), contentAccess(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if page.Page.Visibility != eventContentModel.PageVisibilityPublic {
		ctx.Header("Cache-Control", "private, no-store")
	}
	response.AbortWithData(ctx, toPublicEventPageResponse(page))
}

func (h *Handler) publicPageAccess(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	if err := h.useCase.RequirePublicEventPage(ctx, eventID, ctx.Param("slug")); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.Status(204)
	ctx.Writer.WriteHeaderNow()
}

func (h *Handler) publicPageDocument(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.GetPublicEventPage(ctx, eventID, ctx.Param("slug"), eventUseCase.ContentAccess{Role: rbac.RolePublic})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, struct {
		Page any `json:"Page"`
	}{Page: page.Page})
}

func (h *Handler) publicPageValues(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.GetPublicEventPage(ctx, eventID, ctx.Param("slug"), eventUseCase.ContentAccess{Role: rbac.RolePublic})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, struct {
		Variables map[string]any `json:"Variables"`
	}{Variables: page.Variables})
}
