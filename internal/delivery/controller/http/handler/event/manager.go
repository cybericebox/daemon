package event

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// listManagers godoc
// @Summary List event managers
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]eventManagerResponse}
// @Router /events/{id}/managers [get]
func (h *Handler) listManagers(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListEventManagers(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventManagerResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toEventManagerResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// setManager godoc
// @Summary Grant or change a non-owner event manager role
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param userID path string true "user ID"
// @Param body body setEventManagerRequest true "manager role"
// @Success 200 {object} response.Response{data=eventManagerResponse}
// @Router /events/{id}/managers/{userID} [put]
func (h *Handler) setManager(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req setEventManagerRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.SetEventManager(ctx, eventID, eventUseCase.SetEventManagerInput{UserID: userID, Role: req.Role})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventManagerResponse(value))
}

// removeManager godoc
// @Summary Remove a non-owner event manager
// @Tags events
// @Param id path string true "event ID"
// @Param userID path string true "user ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/managers/{userID} [delete]
func (h *Handler) removeManager(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.RemoveEventManager(ctx, eventID, userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
