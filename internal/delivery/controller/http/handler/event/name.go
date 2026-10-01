package event

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type eventPublicNameRequest struct {
	Name string `json:"Name"`
}

type eventPublicNameResponse struct {
	Name string `json:"Name"`
}

// getPublicName godoc
// @Summary Get the event's participant-visible name
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=eventPublicNameResponse}
// @Router /events/{id}/manage/name [get]
func (h *Handler) getPublicName(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	name, err := h.useCase.GetEventPublicName(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventPublicNameResponse{Name: name})
}

// updatePublicName godoc
// @Summary Update the event's participant-visible name
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body eventPublicNameRequest true "public name"
// @Success 200 {object} response.Response{data=eventPublicNameResponse}
// @Router /events/{id}/manage/name [put]
func (h *Handler) updatePublicName(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req eventPublicNameRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	name, err := h.useCase.UpdateEventPublicName(ctx, id, req.Name, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventPublicNameResponse{Name: name})
}
