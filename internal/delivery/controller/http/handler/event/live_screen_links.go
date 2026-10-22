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
	// Link is null when the screen has no working link.
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
// @Summary Get the screen link of the event's first screen (without its token)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=liveScreenLinkStateResponse}
// @Router /events/{id}/manage/content/live/screen-link [get]
func (h *Handler) getLiveScreenLink(ctx *gin.Context) {
	h.serveLiveScreenLink(ctx, uuid.Nil)
}

// getLiveScreenLinkOf godoc
// @Summary Get a screen's link (without its token)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Success 200 {object} response.Response{data=liveScreenLinkStateResponse}
// @Router /events/{id}/manage/content/live/screens/{screenID}/link [get]
func (h *Handler) getLiveScreenLinkOf(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.serveLiveScreenLink(ctx, screenID)
	}
}

func (h *Handler) serveLiveScreenLink(ctx *gin.Context, screenID uuid.UUID) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	link, err := h.useCase.GetLiveScreenLink(ctx, eventID, screenID)
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
// @Summary Create the screen link of the event's first screen (replaces an existing one; the token is returned once)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body issueLiveScreenLinkRequest true "expiry"
// @Success 200 {object} response.Response{data=liveScreenLinkResponse}
// @Router /events/{id}/manage/content/live/screen-link [post]
func (h *Handler) issueLiveScreenLink(ctx *gin.Context) {
	h.serveIssueLiveScreenLink(ctx, uuid.Nil)
}

// issueLiveScreenLinkOf godoc
// @Summary Create a screen's link (replaces its existing one; the token is returned once)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Param body body issueLiveScreenLinkRequest true "expiry"
// @Success 200 {object} response.Response{data=liveScreenLinkResponse}
// @Router /events/{id}/manage/content/live/screens/{screenID}/link [post]
func (h *Handler) issueLiveScreenLinkOf(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.serveIssueLiveScreenLink(ctx, screenID)
	}
}

func (h *Handler) serveIssueLiveScreenLink(ctx *gin.Context, screenID uuid.UUID) {
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
	link, err := h.useCase.IssueLiveScreenLink(ctx, eventID, screenID, claims.UserID, req.Expiry)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLiveScreenLinkResponse(link.LiveScreenLinkView, link.Token))
}

// regenerateLiveScreenLink godoc
// @Summary Replace the first screen's link token (same expiry); the old URL stops working
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=liveScreenLinkResponse}
// @Router /events/{id}/manage/content/live/screen-link/regenerate [post]
func (h *Handler) regenerateLiveScreenLink(ctx *gin.Context) {
	h.serveRegenerateLiveScreenLink(ctx, uuid.Nil)
}

// regenerateLiveScreenLinkOf godoc
// @Summary Replace a screen's link token (same expiry); the old URL stops working
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Success 200 {object} response.Response{data=liveScreenLinkResponse}
// @Router /events/{id}/manage/content/live/screens/{screenID}/link/regenerate [post]
func (h *Handler) regenerateLiveScreenLinkOf(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.serveRegenerateLiveScreenLink(ctx, screenID)
	}
}

func (h *Handler) serveRegenerateLiveScreenLink(ctx *gin.Context, screenID uuid.UUID) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	link, err := h.useCase.RegenerateLiveScreenLink(ctx, eventID, screenID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLiveScreenLinkResponse(link.LiveScreenLinkView, link.Token))
}

// revokeLiveScreenLink godoc
// @Summary Turn the first screen's link off («Вимкнути»)
// @Tags events
// @Param id path string true "event ID"
// @Success 204
// @Router /events/{id}/manage/content/live/screen-link [delete]
func (h *Handler) revokeLiveScreenLink(ctx *gin.Context) {
	h.serveRevokeLiveScreenLink(ctx, uuid.Nil)
}

// revokeLiveScreenLinkOf godoc
// @Summary Turn a screen's link off («Вимкнути»)
// @Tags events
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Success 204
// @Router /events/{id}/manage/content/live/screens/{screenID}/link [delete]
func (h *Handler) revokeLiveScreenLinkOf(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.serveRevokeLiveScreenLink(ctx, screenID)
	}
}

func (h *Handler) serveRevokeLiveScreenLink(ctx *gin.Context, screenID uuid.UUID) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.RevokeLiveScreenLink(ctx, eventID, screenID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithNoContent(ctx)
}
