package eventself

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// publicReadFreshFor is how long a browser reuses an anonymous answer of a public read before it asks again.
const publicReadFreshFor = 30 * time.Second

// anonymousFreshFor is publicReadFreshFor for a caller with no session, and 0 (always revalidate) for a
// signed-in one, whose answer may differ (staff see more).
func anonymousFreshFor(ctx *gin.Context) time.Duration {
	if _, signedIn := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); signedIn {
		return 0
	}
	return publicReadFreshFor
}

func contentAccess(ctx *gin.Context) eventUseCase.ContentAccess {
	access := eventUseCase.ContentAccess{Role: rbac.RolePublic}
	if claims, found := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); found {
		access.UserID, access.Role = &claims.UserID, claims.Role
	}
	return access
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
	response.AbortWithETagData(ctx, pages, anonymousFreshFor(ctx))
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
