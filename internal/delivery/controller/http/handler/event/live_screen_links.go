package event

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type liveScreenLinkResponse struct {
	ID        uuid.UUID `json:"ID"`
	CreatedAt time.Time `json:"CreatedAt"`
	// ExpiresAt null = no expiry.
	ExpiresAt *time.Time `json:"ExpiresAt"`
	// Token is present only right after the link is issued or regenerated.
	Token string `json:"Token,omitempty"`
}

type liveScreenLinkStateResponse struct {
	// Link is null when the event has no working screen link.
	Link *liveScreenLinkResponse `json:"Link"`
}

type issueLiveScreenLinkRequest struct {
	// Expiry: none | day | week | event_end.
	Expiry eventContentModel.LiveScreenExpiry `json:"Expiry" binding:"required"`
}

func toLiveScreenLinkResponse(v eventUseCase.LiveScreenLinkView, token string) liveScreenLinkResponse {
	return liveScreenLinkResponse{ID: v.ID, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt, Token: token}
}

// getLiveScreenLink godoc
// @Summary Get the event's screen link (without its token)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=liveScreenLinkStateResponse}
// @Router /events/{id}/manage/content/live/screen-link [get]
func (h *Handler) getLiveScreenLink(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	link, err := h.useCase.GetLiveScreenLink(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	state := liveScreenLinkStateResponse{}
	if link != nil {
		value := toLiveScreenLinkResponse(*link, "")
		state.Link = &value
	}
	response.AbortWithData(ctx, state)
}

// issueLiveScreenLink godoc
// @Summary Create the event's screen link (replaces an existing one; the token is returned once)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body issueLiveScreenLinkRequest true "expiry"
// @Success 200 {object} response.Response{data=liveScreenLinkResponse}
// @Router /events/{id}/manage/content/live/screen-link [post]
func (h *Handler) issueLiveScreenLink(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req issueLiveScreenLinkRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	link, err := h.useCase.IssueLiveScreenLink(ctx, eventID, claims.UserID, req.Expiry)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLiveScreenLinkResponse(link.LiveScreenLinkView, link.Token))
}

// regenerateLiveScreenLink godoc
// @Summary Replace the screen link's token (same expiry); the old URL stops working
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=liveScreenLinkResponse}
// @Router /events/{id}/manage/content/live/screen-link/regenerate [post]
func (h *Handler) regenerateLiveScreenLink(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	link, err := h.useCase.RegenerateLiveScreenLink(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLiveScreenLinkResponse(link.LiveScreenLinkView, link.Token))
}

// revokeLiveScreenLink godoc
// @Summary Turn the screen link off («Вимкнути»)
// @Tags events
// @Param id path string true "event ID"
// @Success 204
// @Router /events/{id}/manage/content/live/screen-link [delete]
func (h *Handler) revokeLiveScreenLink(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.RevokeLiveScreenLink(ctx, eventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithNoContent(ctx)
}
